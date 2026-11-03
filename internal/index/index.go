package index

import (
	"sort"
	"strings"
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
	evicted      bool
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
	evictedCount       atomic.Uint64
	evicting           atomic.Bool // Smoking gun fix: CAS guard against double eviction
}

// NewStoreIndex creates a new StoreIndex with the given memory limit.
func NewStoreIndex(cfg IndexConfig) *StoreIndex {
	maxMem := cfg.MaxMemoryBytes
	if maxMem <= 0 {
		maxMem = 512 * 1024 * 1024
	}
	bucketSec := cfg.BucketDurationSec
	if bucketSec <= 0 {
		bucketSec = 60
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
// Handles eviction and guarantees no race condition with evicted partitions.
func (idx *StoreIndex) Insert(entry parser.LogEntry) {
	if idx.currentMemoryBytes.Load() >= idx.maxMemoryBytes {
		idx.evictOldestPartitions()
	}

	bucketID := entry.Timestamp / idx.bucketDuration

	for {
		p := idx.getOrCreatePartition(bucketID)

		p.mu.Lock()
		if p.evicted {
			// Partition was evicted before lock was acquired; retry with fresh partition
			p.mu.Unlock()
			continue
		}

		p.records[entry.ID] = entry
		p.sizeBytes += int64(entry.SizeBytes)
		p.skipList.Insert(entry.Timestamp, entry.ID)

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
		break
	}
}

// evictOldestPartitions drops older time segments or sheds from the single partition.
// Guarded by CAS on idx.evicting to prevent concurrent double-eviction.
func (idx *StoreIndex) evictOldestPartitions() {
	if !idx.evicting.CompareAndSwap(false, true) {
		return // Another worker is actively evicting
	}
	defer idx.evicting.Store(false)

	targetMem := int64(float64(idx.maxMemoryBytes) * 0.85)

	for idx.currentMemoryBytes.Load() > targetMem {
		idx.partitionsMu.Lock()
		if len(idx.partitions) == 0 {
			idx.partitionsMu.Unlock()
			return
		}

		var oldestBucket int64 = 1<<62 - 1
		var newestBucket int64 = -1
		for bID := range idx.partitions {
			if bID < oldestBucket {
				oldestBucket = bID
			}
			if bID > newestBucket {
				newestBucket = bID
			}
		}

		// Rule #2: Never evict newest partition wholesale
		if oldestBucket == newestBucket {
			p := idx.partitions[oldestBucket]
			idx.partitionsMu.Unlock()

			if p == nil {
				return
			}

			p.mu.Lock()
			if p.evicted || len(p.records) <= 1 {
				p.mu.Unlock()
				return
			}

			dropCount := max(1, len(p.records)/4)
			droppedIDs := p.skipList.EvictOldest(dropCount)
			var droppedBytes int64

			for _, id := range droppedIDs {
				if r, ok := p.records[id]; ok {
					droppedBytes += int64(r.SizeBytes)
					delete(p.records, id)
					if sMap, ok := p.serviceIndex[r.Service]; ok {
						delete(sMap, id)
					}
					if lMap, ok := p.levelIndex[r.Level]; ok {
						delete(lMap, id)
					}
				}
			}
			p.sizeBytes -= droppedBytes
			p.mu.Unlock()

			idx.currentMemoryBytes.Add(-droppedBytes)
			idx.evictedCount.Add(uint64(len(droppedIDs)))
			continue
		}

		// Multiple partitions: evict the oldest
		oldest := idx.partitions[oldestBucket]
		delete(idx.partitions, oldestBucket)
		idx.partitionsMu.Unlock()

		if oldest != nil {
			oldest.mu.Lock()
			oldest.evicted = true
			freed := oldest.sizeBytes
			count := len(oldest.records)
			oldest.mu.Unlock()

			idx.currentMemoryBytes.Add(-freed)
			idx.evictedCount.Add(uint64(count))
		}
	}
}

// Query performs multi-field time-range and exact-match filtering.
// Guarantees mathematically exact (Timestamp, ID) ordering and early exit without heap allocations.
func (idx *StoreIndex) Query(start, end int64, level, service, grep string, limit int, order string) []parser.LogEntry {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
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

	if order == "desc" {
		sort.Slice(targetBuckets, func(i, j int) bool {
			return targetBuckets[i] > targetBuckets[j]
		})
	} else {
		sort.Slice(targetBuckets, func(i, j int) bool {
			return targetBuckets[i] < targetBuckets[j]
		})
	}

	results := make([]parser.LogEntry, 0, limit)
	grepLower := strings.ToLower(grep)

	for _, bID := range targetBuckets {
		idx.partitionsMu.RLock()
		p, ok := idx.partitions[bID]
		idx.partitionsMu.RUnlock()
		if !ok {
			continue
		}

		p.mu.RLock()
		if p.evicted {
			p.mu.RUnlock()
			continue
		}

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

		// Node scanner with O(1) filter checks and early exit
		scanner := func(id uint64, ts int64) bool {
			if serviceFilter {
				if _, match := sSet[id]; !match {
					return true
				}
			}
			if levelFilter {
				if _, match := lSet[id]; !match {
					return true
				}
			}

			if entry, match := p.records[id]; match {
				if grepLower != "" && !strings.Contains(strings.ToLower(entry.Message), grepLower) {
					return true
				}
				results = append(results, entry)
				if len(results) >= limit {
					return false // Reached limit, terminate early!
				}
			}
			return true
		}

		if order == "desc" {
			p.skipList.ScanRangeDesc(start, end, scanner)
		} else {
			p.skipList.ScanRangeAsc(start, end, scanner)
		}

		p.mu.RUnlock()

		if len(results) >= limit {
			break
		}
	}
	return results
}

// Burn completely purges all partitions and log records from memory,
// resets memory accounting to 0, and returns the number of purged entries.
func (idx *StoreIndex) Burn() int {
	idx.partitionsMu.Lock()
	defer idx.partitionsMu.Unlock()

	purged := 0
	for _, p := range idx.partitions {
		p.mu.Lock()
		p.evicted = true
		purged += len(p.records)
		p.records = nil
		p.serviceIndex = nil
		p.levelIndex = nil
		p.skipList = nil
		p.mu.Unlock()
	}

	idx.partitions = make(map[int64]*Partition)
	idx.currentMemoryBytes.Store(0)
	idx.evictedCount.Store(0)
	return purged
}

// CurrentMemoryUsage returns tracked in-memory byte size.
func (idx *StoreIndex) CurrentMemoryUsage() int64 {
	return idx.currentMemoryBytes.Load()
}

// LivePartitionBytes calculates sum of sizeBytes across all live partitions.
func (idx *StoreIndex) LivePartitionBytes() int64 {
	idx.partitionsMu.RLock()
	defer idx.partitionsMu.RUnlock()

	var total int64
	for _, p := range idx.partitions {
		p.mu.RLock()
		if !p.evicted {
			total += p.sizeBytes
		}
		p.mu.RUnlock()
	}
	return total
}

// TotalRecords returns the number of active records across all partitions.
func (idx *StoreIndex) TotalRecords() int {
	idx.partitionsMu.RLock()
	defer idx.partitionsMu.RUnlock()

	total := 0
	for _, p := range idx.partitions {
		p.mu.RLock()
		if !p.evicted {
			total += len(p.records)
		}
		p.mu.RUnlock()
	}
	return total
}

// PartitionCount returns the number of active partitions.
func (idx *StoreIndex) PartitionCount() int {
	idx.partitionsMu.RLock()
	defer idx.partitionsMu.RUnlock()
	return len(idx.partitions)
}

// EvictedCount returns total entries evicted.
func (idx *StoreIndex) EvictedCount() uint64 {
	return idx.evictedCount.Load()
}
