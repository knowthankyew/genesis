package index

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/knowthankyew/genesis/internal/parser"
)

// IndexConfig defines capacity and memory ceiling parameters.
type IndexConfig struct {
	MaxMemoryBytes    int64
	BucketDurationSec int64
}

// Partition represents a time-bounded log segment.
type Partition struct {
	mu           sync.RWMutex
	bucketID     int64
	startTime    int64
	endTime      int64
	skipList     *SkipList
	serviceIndex map[string]map[uint64]struct{}
	levelIndex   map[string]map[uint64]struct{}
	records      map[uint64]parser.LogEntry
	sizeBytes    int64
}

func newPartition(bucketID, bucketDuration int64) *Partition {
	start := bucketID * bucketDuration
	return &Partition{
		bucketID:     bucketID,
		startTime:    start,
		endTime:      start + bucketDuration - 1,
		skipList:     NewSkipList(),
		serviceIndex: make(map[string]map[uint64]struct{}),
		levelIndex:   make(map[string]map[uint64]struct{}),
		records:      make(map[uint64]parser.LogEntry),
	}
}

// StoreIndex is the primary in-memory time-partitioned indexing and storage engine.
type StoreIndex struct {
	partitionsMu   sync.RWMutex
	partitions     map[int64]*Partition
	bucketDuration int64

	currentMemoryBytes atomic.Int64
	maxMemoryBytes     int64
	evicting           atomic.Bool
}

// NewStoreIndex creates a new StoreIndex with the given memory limit.
func NewStoreIndex(cfg IndexConfig) *StoreIndex {
	maxMem := cfg.MaxMemoryBytes
	if maxMem <= 0 {
		maxMem = 512 * 1024 * 1024 // default 512MB
	}
	bucketSec := cfg.BucketDurationSec
	if bucketSec <= 0 {
		bucketSec = 60 // 60-second partitions
	}

	return &StoreIndex{
		partitions:     make(map[int64]*Partition),
		bucketDuration: bucketSec,
		maxMemoryBytes: maxMem,
	}
}

func (idx *StoreIndex) getOrCreatePartition(bucketID int64) *Partition {
	idx.partitionsMu.RLock()
	p, ok := idx.partitions[bucketID]
	idx.partitionsMu.RUnlock()
	if ok {
		return p
	}

	idx.partitionsMu.Lock()
	defer idx.partitionsMu.Unlock()
	if p, ok = idx.partitions[bucketID]; ok {
		return p
	}

	p = newPartition(bucketID, idx.bucketDuration)
	idx.partitions[bucketID] = p
	return p
}

// Insert adds a LogEntry to the matching time-partitioned index.
// Automatically evicts the oldest time-bucket segment if memory ceiling is breached.
func (idx *StoreIndex) Insert(entry parser.LogEntry) {
	// If memory ceiling breached, evict older segments to stay within bounds
	if idx.currentMemoryBytes.Load() >= idx.maxMemoryBytes {
		idx.evictOldestPartitions()
	}

	bucketID := entry.Timestamp / idx.bucketDuration
	p := idx.getOrCreatePartition(bucketID)

	p.mu.Lock()
	p.records[entry.ID] = entry
	p.sizeBytes += int64(entry.SizeBytes)
	p.skipList.Insert(entry.Timestamp, entry.ID)

	// Inverted indices inside partition
	if entry.Service != "" {
		sMap, ok := p.serviceIndex[entry.Service]
		if !ok {
			sMap = make(map[uint64]struct{})
			p.serviceIndex[entry.Service] = sMap
		}
		sMap[entry.ID] = struct{}{}
	}

	if entry.Level != "" {
		lMap, ok := p.levelIndex[entry.Level]
		if !ok {
			lMap = make(map[uint64]struct{})
			p.levelIndex[entry.Level] = lMap
		}
		lMap[entry.ID] = struct{}{}
	}
	p.mu.Unlock()

	idx.currentMemoryBytes.Add(int64(entry.SizeBytes))
}

// evictOldestPartitions drops oldest time segments until memory drops below threshold.
func (idx *StoreIndex) evictOldestPartitions() {
	targetMem := int64(float64(idx.maxMemoryBytes) * 0.85)

	for idx.currentMemoryBytes.Load() > targetMem {
		idx.partitionsMu.Lock()
		if len(idx.partitions) == 0 {
			idx.partitionsMu.Unlock()
			return
		}

		var oldestBucket int64 = 1<<62 - 1
		for bID := range idx.partitions {
			if bID < oldestBucket {
				oldestBucket = bID
			}
		}

		oldest := idx.partitions[oldestBucket]
		delete(idx.partitions, oldestBucket)
		idx.partitionsMu.Unlock()

		if oldest != nil {
			oldest.mu.RLock()
			freed := oldest.sizeBytes
			oldest.mu.RUnlock()

			idx.currentMemoryBytes.Add(-freed)
		}
	}
}

// Query performs multi-field time-range and exact-match filtering.
// Reads only relevant time partitions with independent reader locks to guarantee zero stalling.
func (idx *StoreIndex) Query(start, end int64, level, service string, limit int) []parser.LogEntry {
	if limit <= 0 {
		limit = 100
	}

	startBucket := start / idx.bucketDuration
	endBucket := end / idx.bucketDuration

	idx.partitionsMu.RLock()
	var targetBuckets []int64
	for bID := range idx.partitions {
		if bID >= startBucket && bID <= endBucket {
			targetBuckets = append(targetBuckets, bID)
		}
	}
	idx.partitionsMu.RUnlock()

	if len(targetBuckets) == 0 {
		return nil
	}

	sort.Slice(targetBuckets, func(i, j int) bool {
		return targetBuckets[i] < targetBuckets[j]
	})

	results := make([]parser.LogEntry, 0, limit)

	for _, bID := range targetBuckets {
		idx.partitionsMu.RLock()
		p, ok := idx.partitions[bID]
		idx.partitionsMu.RUnlock()
		if !ok {
			continue
		}

		p.mu.RLock()
		var sSet, lSet map[uint64]struct{}
		serviceFilter := service != ""
		levelFilter := level != ""

		if serviceFilter {
			sSet, ok = p.serviceIndex[service]
			if !ok || len(sSet) == 0 {
				p.mu.RUnlock()
				continue
			}
		}

		if levelFilter {
			lSet, ok = p.levelIndex[level]
			if !ok || len(lSet) == 0 {
				p.mu.RUnlock()
				continue
			}
		}

		// Gemini Spec: "Inverted Indexing: Build a secondary inverted index for LogLevel
		// and ServiceName to allow instant O(1) or O(log N) bucket lookups BEFORE executing a time-range filter."
		if serviceFilter || levelFilter {
			// Choose the smallest inverted index bucket to drive the lookup
			var driverSet map[uint64]struct{}
			var otherFilter map[uint64]struct{}

			if serviceFilter && levelFilter {
				if len(sSet) <= len(lSet) {
					driverSet = sSet
					otherFilter = lSet
				} else {
					driverSet = lSet
					otherFilter = sSet
				}
			} else if serviceFilter {
				driverSet = sSet
			} else {
				driverSet = lSet
			}

			for id := range driverSet {
				if otherFilter != nil {
					if _, match := otherFilter[id]; !match {
						continue
					}
				}
				if entry, match := p.records[id]; match {
					if entry.Timestamp >= start && entry.Timestamp <= end {
						results = append(results, entry)
						if len(results) >= limit {
							break
						}
					}
				}
			}
		} else {
			// Pure time-range query without field filters: scan the SkipList
			p.skipList.ScanRange(start, end, func(id uint64) bool {
				if entry, match := p.records[id]; match {
					results = append(results, entry)
					if len(results) >= limit {
						return false // stop early
					}
				}
				return true
			})
		}

		p.mu.RUnlock()

		if len(results) >= limit {
			break
		}
	}

	return results
}

// CurrentMemoryUsage returns the currently tracked in-memory byte size.
func (idx *StoreIndex) CurrentMemoryUsage() int64 {
	return idx.currentMemoryBytes.Load()
}

// TotalRecords returns the number of active records across all partitions.
func (idx *StoreIndex) TotalRecords() int {
	idx.partitionsMu.RLock()
	defer idx.partitionsMu.RUnlock()

	total := 0
	for _, p := range idx.partitions {
		p.mu.RLock()
		total += len(p.records)
		p.mu.RUnlock()
	}
	return total
}
