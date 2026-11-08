package index

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/knowthankyew/genesis/internal/parser"
)

const (
	DefaultMaxMemoryBytes            = 512 * 1024 * 1024
	DefaultQueryLimit                = 100
	MaxQueryLimit                    = 10000
	NumPartitionShards               = 8
	MaxChunkEntries                  = 4096
	GCWatermarkThresholdBytes        = 64 * 1024 * 1024
	EvictionTriggerPercent           = 52
	EvictionTargetPercent            = 45
	SmallBudgetEvictionTargetPercent = 85
)

// IndexConfig defines capacity and memory ceiling parameters.
type IndexConfig struct {
	MaxMemoryBytes    int64
	BucketDurationSec int64
}

type chunkPosting struct {
	ts     int64
	id     uint64
	recIdx uint32
}

type svcLevelKey struct {
	service string
	level   string
}

func comparePosting(a, b chunkPosting) int {
	if a.ts < b.ts {
		return -1
	}
	if a.ts > b.ts {
		return 1
	}
	if a.id < b.id {
		return -1
	}
	if a.id > b.id {
		return 1
	}
	return 0
}

// partChunk is a bounded micro-segment of log entries stored in a flat contiguous slice.
// Once sealed and moved to Partition.sealed, a partChunk is 100% immutable,
// queried lock-free with O(1) direct slice indexing, and evicted in O(1) via pointer drop.
type partChunk struct {
	skipList   *SkipList
	byService  map[string][]chunkPosting
	byLevel    map[string][]chunkPosting
	bySvcLevel map[svcLevelKey][]chunkPosting
	entries    []parser.LogEntry
	liveCount  int
	sizeBytes  int64
	minTs      int64
	minID      uint64
	maxTs      int64
	maxID      uint64
	isSealed   bool
}

func newPartChunk() *partChunk {
	return &partChunk{
		skipList:   NewSkipList(),
		byService:  make(map[string][]chunkPosting, 8),
		byLevel:    make(map[string][]chunkPosting, 8),
		bySvcLevel: make(map[svcLevelKey][]chunkPosting, 16),
		entries:    make([]parser.LogEntry, 0, 256),
	}
}

func (c *partChunk) insert(entry parser.LogEntry) {
	if c.liveCount == 0 {
		c.minTs, c.minID = entry.Timestamp, entry.ID
		c.maxTs, c.maxID = entry.Timestamp, entry.ID
	} else {
		if entry.Timestamp < c.minTs || (entry.Timestamp == c.minTs && entry.ID < c.minID) {
			c.minTs, c.minID = entry.Timestamp, entry.ID
		}
		if entry.Timestamp > c.maxTs || (entry.Timestamp == c.maxTs && entry.ID > c.maxID) {
			c.maxTs, c.maxID = entry.Timestamp, entry.ID
		}
	}

	recIdx := uint32(len(c.entries))
	c.entries = append(c.entries, entry)
	c.liveCount++
	c.sizeBytes += int64(entry.SizeBytes)
	c.skipList.InsertWithIdx(entry.Timestamp, entry.ID, recIdx)

	p := chunkPosting{ts: entry.Timestamp, id: entry.ID, recIdx: recIdx}
	if entry.Service != "" {
		c.byService[entry.Service] = append(c.byService[entry.Service], p)
	}
	if entry.Level != "" {
		c.byLevel[entry.Level] = append(c.byLevel[entry.Level], p)
	}
	if entry.Service != "" && entry.Level != "" {
		key := svcLevelKey{service: entry.Service, level: entry.Level}
		c.bySvcLevel[key] = append(c.bySvcLevel[key], p)
	}
}

func (c *partChunk) sealInPlace() {
	for _, s := range c.byService {
		slices.SortFunc(s, comparePosting)
	}
	for _, s := range c.byLevel {
		slices.SortFunc(s, comparePosting)
	}
	for _, s := range c.bySvcLevel {
		slices.SortFunc(s, comparePosting)
	}
}

func compactPostings(s []chunkPosting, entries []parser.LogEntry) []chunkPosting {
	out := s[:0]
	for _, p := range s {
		if int(p.recIdx) < len(entries) && entries[p.recIdx].SizeBytes >= 0 {
			out = append(out, p)
		}
	}
	return out
}

func (c *partChunk) compactActivePostings() {
	for k, s := range c.byService {
		compacted := compactPostings(s, c.entries)
		if len(compacted) == 0 {
			delete(c.byService, k)
		} else {
			c.byService[k] = compacted
		}
	}
	for k, s := range c.byLevel {
		compacted := compactPostings(s, c.entries)
		if len(compacted) == 0 {
			delete(c.byLevel, k)
		} else {
			c.byLevel[k] = compacted
		}
	}
	for k, s := range c.bySvcLevel {
		compacted := compactPostings(s, c.entries)
		if len(compacted) == 0 {
			delete(c.bySvcLevel, k)
		} else {
			c.bySvcLevel[k] = compacted
		}
	}
}

type partShard struct {
	mu      sync.RWMutex
	active  *partChunk
	sealing []*partChunk
	evicted bool
}

// Partition represents a time-bounded log bucket composed of sharded active
// chunks and immutable sealed micro-segments.
// Lock ordering hierarchy: shards[0].mu -> ... -> shards[7].mu -> sealedMu.
type Partition struct {
	bucketID  int64
	startTime int64
	endTime   int64

	shards [NumPartitionShards]partShard

	sealedMu sync.RWMutex
	sealed   []*partChunk
	evicted  bool
}

func newPartition(bucketID, bucketDuration int64) *Partition {
	start := bucketID * bucketDuration
	p := &Partition{
		bucketID:  bucketID,
		startTime: start,
		endTime:   start + bucketDuration - 1,
	}
	for i := 0; i < NumPartitionShards; i++ {
		p.shards[i].active = newPartChunk()
	}
	return p
}

func (p *Partition) markEvictedAndDrain() (freedBytes int64, evictedRecords int) {
	for i := 0; i < NumPartitionShards; i++ {
		p.shards[i].mu.Lock()
	}
	p.sealedMu.Lock()

	if !p.evicted {
		p.evicted = true
		for _, c := range p.sealed {
			freedBytes += c.sizeBytes
			evictedRecords += c.liveCount
		}
		p.sealed = nil

		for i := 0; i < NumPartitionShards; i++ {
			p.shards[i].evicted = true
			if c := p.shards[i].active; c != nil {
				freedBytes += c.sizeBytes
				evictedRecords += c.liveCount
				p.shards[i].active = nil
			}
			for _, sc := range p.shards[i].sealing {
				freedBytes += sc.sizeBytes
				evictedRecords += sc.liveCount
			}
			p.shards[i].sealing = nil
		}
	}

	p.sealedMu.Unlock()
	for i := NumPartitionShards - 1; i >= 0; i-- {
		p.shards[i].mu.Unlock()
	}
	return freedBytes, evictedRecords
}

// StoreIndex is the primary in-memory time-partitioned indexing and storage engine.
type StoreIndex struct {
	partitionsMu   sync.RWMutex
	partitions     map[int64]*Partition
	bucketDuration int64

	currentMemoryBytes atomic.Int64
	maxMemoryBytes     int64
	evictedCount       atomic.Uint64
	evicting           atomic.Bool // CAS guard against concurrent double eviction
}

// NewStoreIndex creates a new StoreIndex with the given memory limit.
func NewStoreIndex(cfg IndexConfig) *StoreIndex {
	maxMem := cfg.MaxMemoryBytes
	if maxMem <= 0 {
		maxMem = DefaultMaxMemoryBytes
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
	evictTrigger := idx.maxMemoryBytes
	if evictTrigger >= GCWatermarkThresholdBytes {
		// Leave GC sweep headroom so dying evicted partitions + live heap stay well below runtime ceiling
		evictTrigger = (idx.maxMemoryBytes * EvictionTriggerPercent) / 100
	}
	if idx.currentMemoryBytes.Load() >= evictTrigger {
		idx.evictOldestPartitions()
	}

	bucketID := entry.Timestamp / idx.bucketDuration
	shardIdx := entry.ID & (NumPartitionShards - 1)

	for {
		p := idx.getOrCreatePartition(bucketID)
		sh := &p.shards[shardIdx]

		sh.mu.Lock()
		if sh.evicted {
			// Partition was evicted before lock was acquired; retry with fresh partition
			sh.mu.Unlock()
			continue
		}

		c := sh.active
		c.insert(entry)
		idx.currentMemoryBytes.Add(int64(entry.SizeBytes))

		var toSeal *partChunk
		if len(c.entries) >= MaxChunkEntries {
			// Move full chunk to sh.sealing and open a fresh active chunk immediately;
			// in-place posting sort runs outside sh.mu while readers use skipList+entries on sh.sealing.
			sh.sealing = append(sh.sealing, c)
			sh.active = newPartChunk()
			toSeal = c
		}

		sh.mu.Unlock()

		if toSeal != nil {
			toSeal.sealInPlace()
			sh.mu.Lock()
			p.sealedMu.Lock()
			for i, sc := range sh.sealing {
				if sc == toSeal {
					last := len(sh.sealing) - 1
					sh.sealing[i] = sh.sealing[last]
					sh.sealing[last] = nil
					sh.sealing = sh.sealing[:last]
					break
				}
			}
			toSeal.isSealed = true
			if !p.evicted && !sh.evicted {
				p.sealed = append(p.sealed, toSeal)
			}
			p.sealedMu.Unlock()
			sh.mu.Unlock()
		}
		break
	}
}

// evictOldestPartitions drops older time buckets or O(1)-drops sealed micro-segments
// from the active bucket without per-entry map iteration under lock.
func (idx *StoreIndex) evictOldestPartitions() {
	if !idx.evicting.CompareAndSwap(false, true) {
		return // Another worker is actively evicting
	}
	defer idx.evicting.Store(false)

	var targetMem int64
	if idx.maxMemoryBytes >= GCWatermarkThresholdBytes {
		targetMem = (idx.maxMemoryBytes * EvictionTargetPercent) / 100
	} else {
		targetMem = (idx.maxMemoryBytes * SmallBudgetEvictionTargetPercent) / 100
	}

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

			// Fast path: O(1) pointer-drop of oldest immutable sealed chunks
			p.sealedMu.Lock()
			if p.evicted {
				p.sealedMu.Unlock()
				return
			}
			if len(p.sealed) > 0 {
				var freed int64
				var count uint64
				curMem := idx.currentMemoryBytes.Load()
				dropIdx := 0
				for dropIdx < len(p.sealed) && (curMem-freed) > targetMem {
					c := p.sealed[dropIdx]
					freed += c.sizeBytes
					count += uint64(c.liveCount)
					p.sealed[dropIdx] = nil
					dropIdx++
				}
				if dropIdx > 0 {
					n := copy(p.sealed, p.sealed[dropIdx:])
					for i := n; i < len(p.sealed); i++ {
						p.sealed[i] = nil
					}
					p.sealed = p.sealed[:n]
				}
				p.sealedMu.Unlock()

				if freed > 0 {
					idx.currentMemoryBytes.Add(-freed)
					idx.evictedCount.Add(count)
					continue
				}
				return
			}
			p.sealedMu.Unlock()

			// Fallback for tiny-memory configurations before any chunk seals:
			// shed oldest entries from the most populated active shard.
			bestShard := -1
			maxRecs := 0
			for i := 0; i < NumPartitionShards; i++ {
				sh := &p.shards[i]
				sh.mu.RLock()
				if !sh.evicted && sh.active != nil && sh.active.liveCount > maxRecs {
					maxRecs = sh.active.liveCount
					bestShard = i
				}
				sh.mu.RUnlock()
			}
			if bestShard == -1 || maxRecs <= 0 {
				return
			}

			// Count total live records across shards so we never empty the last record in the partition
			if maxRecs == 1 {
				totalActive := 0
				for i := 0; i < NumPartitionShards; i++ {
					sh := &p.shards[i]
					sh.mu.RLock()
					if !sh.evicted && sh.active != nil {
						totalActive += sh.active.liveCount
					}
					sh.mu.RUnlock()
				}
				if totalActive <= 1 {
					return
				}
			}

			sh := &p.shards[bestShard]
			sh.mu.Lock()
			if sh.evicted || sh.active == nil || sh.active.liveCount == 0 {
				sh.mu.Unlock()
				return
			}
			c := sh.active
			dropCount := max(1, c.liveCount/4)
			droppedIdxs := c.skipList.EvictOldestIdx(dropCount)
			var droppedBytes int64
			var droppedLive uint64
			for _, recIdx := range droppedIdxs {
				if int(recIdx) < len(c.entries) && c.entries[recIdx].SizeBytes >= 0 {
					droppedBytes += int64(c.entries[recIdx].SizeBytes)
					c.entries[recIdx] = parser.LogEntry{SizeBytes: -1}
					c.liveCount--
					droppedLive++
				}
			}
			c.compactActivePostings()
			c.sizeBytes -= droppedBytes
			if fTs, fID, ok := c.skipList.First(); ok {
				c.minTs, c.minID = fTs, fID
			}
			sh.mu.Unlock()

			if droppedBytes > 0 {
				idx.currentMemoryBytes.Add(-droppedBytes)
				idx.evictedCount.Add(droppedLive)
				continue
			}
			return
		}

		// Multiple partitions: evict the oldest bucket in O(1)
		oldest := idx.partitions[oldestBucket]
		delete(idx.partitions, oldestBucket)
		idx.partitionsMu.Unlock()

		if oldest != nil {
			freed, count := oldest.markEvictedAndDrain()
			if freed > 0 || count > 0 {
				idx.currentMemoryBytes.Add(-freed)
				idx.evictedCount.Add(uint64(count))
			}
		}
	}
}

// topKCollector maintains the globally best `limit` LogEntry items across chunks
// using a zero-allocation bounded binary heap:
//   - desc == false ("asc"): max-heap where items[0] is the LARGEST (worst) of the `limit` smallest entries.
//   - desc == true  ("desc"): min-heap where items[0] is the SMALLEST (worst) of the `limit` largest entries.
type topKCollector struct {
	items []parser.LogEntry
	limit int
	desc  bool
}

func newTopKCollector(limit int, desc bool) topKCollector {
	return topKCollector{
		items: make([]parser.LogEntry, 0, min(limit, 256)),
		limit: limit,
		desc:  desc,
	}
}

// isBetter returns true if (tsA, idA) ranks ahead of (tsB, idB) in the target sort order.
func (k *topKCollector) isBetter(tsA int64, idA uint64, tsB int64, idB uint64) bool {
	if k.desc {
		if tsA != tsB {
			return tsA > tsB
		}
		return idA > idB
	}
	if tsA != tsB {
		return tsA < tsB
	}
	return idA < idB
}

func (k *topKCollector) full() bool {
	return len(k.items) >= k.limit
}

func (k *topKCollector) bound() (int64, uint64) {
	return k.items[0].Timestamp, k.items[0].ID
}

func (k *topKCollector) offer(entry parser.LogEntry) {
	if len(k.items) < k.limit {
		k.items = append(k.items, entry)
		k.siftUp(len(k.items) - 1)
		return
	}
	if k.isBetter(entry.Timestamp, entry.ID, k.items[0].Timestamp, k.items[0].ID) {
		k.items[0] = entry
		k.siftDown(0)
	}
}

// heapWorse returns true if items[i] is WORSE than items[j] (so worst item sits at root 0).
func (k *topKCollector) heapWorse(i, j int) bool {
	return k.isBetter(k.items[j].Timestamp, k.items[j].ID, k.items[i].Timestamp, k.items[i].ID)
}

func (k *topKCollector) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !k.heapWorse(i, parent) {
			break
		}
		k.items[i], k.items[parent] = k.items[parent], k.items[i]
		i = parent
	}
}

func (k *topKCollector) siftDown(i int) {
	n := len(k.items)
	for {
		worst := i
		left := 2*i + 1
		right := 2*i + 2
		if left < n && k.heapWorse(left, worst) {
			worst = left
		}
		if right < n && k.heapWorse(right, worst) {
			worst = right
		}
		if worst == i {
			break
		}
		k.items[i], k.items[worst] = k.items[worst], k.items[i]
		i = worst
	}
}

func (k *topKCollector) sorted() []parser.LogEntry {
	sort.Slice(k.items, func(i, j int) bool {
		return k.isBetter(k.items[i].Timestamp, k.items[i].ID, k.items[j].Timestamp, k.items[j].ID)
	})
	return k.items
}

func scanPostings(
	c *partChunk,
	postings []chunkPosting,
	effStart, effEnd int64,
	trace, grepLower string,
	collector *topKCollector,
) {
	n := len(postings)
	if n == 0 {
		return
	}
	entries := c.entries

	if c.isSealed {
		// postings is strictly sorted by (ts asc, id asc)
		if postings[n-1].ts < effStart || postings[0].ts > effEnd {
			return
		}
		if collector.full() {
			bTs, bID := collector.bound()
			if collector.desc {
				last := postings[n-1]
				if !collector.isBetter(last.ts, last.id, bTs, bID) {
					return
				}
			} else {
				first := postings[0]
				if !collector.isBetter(first.ts, first.id, bTs, bID) {
					return
				}
			}
		}

		if collector.desc {
			firstAfter := sort.Search(n, func(i int) bool {
				return postings[i].ts > effEnd
			})
			for i := firstAfter - 1; i >= 0; i-- {
				p := postings[i]
				if p.ts < effStart {
					break
				}
				if collector.full() {
					bTs, bID := collector.bound()
					if !collector.isBetter(p.ts, p.id, bTs, bID) {
						break
					}
				}
				entry := entries[p.recIdx]
				if entry.SizeBytes < 0 {
					continue
				}
				if trace != "" && entry.TraceID != trace {
					continue
				}
				if grepLower != "" &&
					!strings.Contains(strings.ToLower(entry.Message), grepLower) &&
					!strings.Contains(strings.ToLower(entry.TraceID), grepLower) {
					continue
				}
				collector.offer(entry)
			}
		} else {
			startIdx := sort.Search(n, func(i int) bool {
				return postings[i].ts >= effStart
			})
			for i := startIdx; i < n; i++ {
				p := postings[i]
				if p.ts > effEnd {
					break
				}
				if collector.full() {
					bTs, bID := collector.bound()
					if !collector.isBetter(p.ts, p.id, bTs, bID) {
						break
					}
				}
				entry := entries[p.recIdx]
				if entry.SizeBytes < 0 {
					continue
				}
				if trace != "" && entry.TraceID != trace {
					continue
				}
				if grepLower != "" &&
					!strings.Contains(strings.ToLower(entry.Message), grepLower) &&
					!strings.Contains(strings.ToLower(entry.TraceID), grepLower) {
					continue
				}
				collector.offer(entry)
			}
		}
		return
	}

	// Active (unsealed) chunk: small contiguous L1-resident slice scan
	for _, p := range postings {
		if p.ts < effStart || p.ts > effEnd {
			continue
		}
		if collector.full() {
			bTs, bID := collector.bound()
			if !collector.isBetter(p.ts, p.id, bTs, bID) {
				continue
			}
		}
		entry := entries[p.recIdx]
		if entry.SizeBytes < 0 {
			continue
		}
		if trace != "" && entry.TraceID != trace {
			continue
		}
		if grepLower != "" &&
			!strings.Contains(strings.ToLower(entry.Message), grepLower) &&
			!strings.Contains(strings.ToLower(entry.TraceID), grepLower) {
			continue
		}
		collector.offer(entry)
		if collector.full() {
			bTs, _ := collector.bound()
			if collector.desc {
				if bTs > effStart {
					effStart = bTs
				}
			} else {
				if bTs < effEnd {
					effEnd = bTs
				}
			}
		}
	}
}

func scanPartChunk(
	c *partChunk,
	start, end int64,
	level, service, trace, grepLower string,
	inSealing bool,
	collector *topKCollector,
) {
	if c.liveCount == 0 || c.maxTs < start || c.minTs > end {
		return
	}

	// Whole-chunk O(1) bound pruning
	if collector.full() {
		bTs, bID := collector.bound()
		if collector.desc {
			if !collector.isBetter(c.maxTs, c.maxID, bTs, bID) {
				return
			}
		} else {
			if !collector.isBetter(c.minTs, c.minID, bTs, bID) {
				return
			}
		}
	}

	effStart, effEnd := start, end
	if collector.full() {
		bTs, _ := collector.bound()
		if collector.desc {
			if bTs > effStart {
				effStart = bTs
			}
		} else {
			if bTs < effEnd {
				effEnd = bTs
			}
		}
	}

	if !inSealing {
		if service != "" && level != "" {
			postings := c.bySvcLevel[svcLevelKey{service: service, level: level}]
			scanPostings(c, postings, effStart, effEnd, trace, grepLower, collector)
			return
		}
		if service != "" {
			postings := c.byService[service]
			scanPostings(c, postings, effStart, effEnd, trace, grepLower, collector)
			return
		}
		if level != "" {
			postings := c.byLevel[level]
			scanPostings(c, postings, effStart, effEnd, trace, grepLower, collector)
			return
		}
	}

	entries := c.entries
	scanner := func(id uint64, ts int64, recIdx uint32) bool {
		if collector.full() {
			bTs, bID := collector.bound()
			if !collector.isBetter(ts, id, bTs, bID) {
				return false
			}
		}

		entry := entries[recIdx]
		if entry.SizeBytes < 0 {
			return true
		}
		if service != "" && entry.Service != service {
			return true
		}
		if level != "" && entry.Level != level {
			return true
		}
		if trace != "" && entry.TraceID != trace {
			return true
		}
		if grepLower != "" &&
			!strings.Contains(strings.ToLower(entry.Message), grepLower) &&
			!strings.Contains(strings.ToLower(entry.TraceID), grepLower) {
			return true
		}
		collector.offer(entry)
		return true
	}

	if collector.desc {
		c.skipList.ScanRangeDescIdx(effStart, effEnd, scanner)
	} else {
		c.skipList.ScanRangeAscIdx(effStart, effEnd, scanner)
	}
}

// Query performs multi-field time-range and exact-match filtering.
// Guarantees mathematically exact (Timestamp, ID) ordering and early exit.
func (idx *StoreIndex) Query(start, end int64, level, service, grep string, limit int, order string) []parser.LogEntry {
	return idx.QueryWithTrace(start, end, level, service, "", grep, limit, order)
}

// QueryWithTrace performs multi-field time-range, exact-match (level, service, trace), and substring grep filtering.
func (idx *StoreIndex) QueryWithTrace(start, end int64, level, service, trace, grep string, limit int, order string) []parser.LogEntry {
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}

	startBucket := start / idx.bucketDuration
	endBucket := end / idx.bucketDuration

	idx.partitionsMu.RLock()
	var targetBucketsBuf [16]int64
	targetBuckets := targetBucketsBuf[:0]
	for bID := range idx.partitions {
		if bID >= startBucket && bID <= endBucket {
			targetBuckets = append(targetBuckets, bID)
		}
	}
	idx.partitionsMu.RUnlock()

	if len(targetBuckets) == 0 {
		return nil
	}

	isDesc := order == "desc"
	slices.Sort(targetBuckets)
	if isDesc {
		slices.Reverse(targetBuckets)
	}

	results := make([]parser.LogEntry, 0, min(limit, 256))
	var grepLower string
	if grep != "" {
		grepLower = strings.ToLower(grep)
	}

	var seenBuf [32]*partChunk
	var sealedSnapBuf [128]*partChunk
	collector := newTopKCollector(limit, isDesc)
	for _, bID := range targetBuckets {
		idx.partitionsMu.RLock()
		p, ok := idx.partitions[bID]
		idx.partitionsMu.RUnlock()
		if !ok {
			continue
		}

		collector.items = collector.items[:0]
		collector.limit = limit - len(results)

		// 1. Scan active and in-flight sealing chunks per shard first, recording seen pointers.
		// Chunks transition monotonically active -> sealing -> sealed, so scanning shards before
		// snapshotting p.sealed (and skipping seen pointers) prevents missing or duplicating a chunk.
		seen := seenBuf[:0]
		for s := 0; s < NumPartitionShards; s++ {
			sh := &p.shards[s]
			sh.mu.RLock()
			if !sh.evicted {
				if isDesc {
					if sh.active != nil {
						scanPartChunk(sh.active, start, end, level, service, trace, grepLower, false, &collector)
						seen = append(seen, sh.active)
					}
					for _, sc := range sh.sealing {
						scanPartChunk(sc, start, end, level, service, trace, grepLower, true, &collector)
						seen = append(seen, sc)
					}
				} else {
					for _, sc := range sh.sealing {
						scanPartChunk(sc, start, end, level, service, trace, grepLower, true, &collector)
						seen = append(seen, sc)
					}
					if sh.active != nil {
						scanPartChunk(sh.active, start, end, level, service, trace, grepLower, false, &collector)
						seen = append(seen, sh.active)
					}
				}
			}
			sh.mu.RUnlock()
		}

		// 2. Snapshot sealed chunk pointers (sub-microsecond RLock) and scan lock-free
		p.sealedMu.RLock()
		if p.evicted {
			p.sealedMu.RUnlock()
			continue
		}
		var sealedSnap []*partChunk
		if n := len(p.sealed); n > 0 {
			if n <= len(sealedSnapBuf) {
				sealedSnap = sealedSnapBuf[:n]
			} else {
				sealedSnap = make([]*partChunk, n)
			}
			copy(sealedSnap, p.sealed)
		}
		p.sealedMu.RUnlock()

		if isDesc {
			// Newest sealed chunks first for rapid descending bound tightening
			for i := len(sealedSnap) - 1; i >= 0; i-- {
				c := sealedSnap[i]
				if slices.Contains(seen, c) {
					continue
				}
				scanPartChunk(c, start, end, level, service, trace, grepLower, false, &collector)
			}
		} else {
			// Oldest sealed chunks first for rapid ascending bound tightening
			for i := 0; i < len(sealedSnap); i++ {
				c := sealedSnap[i]
				if slices.Contains(seen, c) {
					continue
				}
				scanPartChunk(c, start, end, level, service, trace, grepLower, false, &collector)
			}
		}

		results = append(results, collector.sorted()...)
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
	var totalFreed int64
	for _, p := range idx.partitions {
		freed, count := p.markEvictedAndDrain()
		totalFreed += freed
		purged += count
	}

	idx.partitions = make(map[int64]*Partition)
	idx.currentMemoryBytes.Add(-totalFreed)
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
		for i := 0; i < NumPartitionShards; i++ {
			p.shards[i].mu.RLock()
		}
		p.sealedMu.RLock()
		if !p.evicted {
			for _, c := range p.sealed {
				total += c.sizeBytes
			}
			for i := 0; i < NumPartitionShards; i++ {
				if c := p.shards[i].active; c != nil {
					total += c.sizeBytes
				}
				for _, sc := range p.shards[i].sealing {
					total += sc.sizeBytes
				}
			}
		}
		p.sealedMu.RUnlock()
		for i := NumPartitionShards - 1; i >= 0; i-- {
			p.shards[i].mu.RUnlock()
		}
	}
	return total
}

// TotalRecords returns the number of active records across all partitions.
func (idx *StoreIndex) TotalRecords() int {
	idx.partitionsMu.RLock()
	defer idx.partitionsMu.RUnlock()

	total := 0
	for _, p := range idx.partitions {
		for i := 0; i < NumPartitionShards; i++ {
			p.shards[i].mu.RLock()
		}
		p.sealedMu.RLock()
		if !p.evicted {
			for _, c := range p.sealed {
				total += c.liveCount
			}
			for i := 0; i < NumPartitionShards; i++ {
				if c := p.shards[i].active; c != nil {
					total += c.liveCount
				}
				for _, sc := range p.shards[i].sealing {
					total += sc.liveCount
				}
			}
		}
		p.sealedMu.RUnlock()
		for i := NumPartitionShards - 1; i >= 0; i-- {
			p.shards[i].mu.RUnlock()
		}
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
