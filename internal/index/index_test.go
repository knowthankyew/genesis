package index

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/knowthankyew/genesis/internal/parser"
)

func TestStoreIndex_Basic(t *testing.T) {
	idx := NewStoreIndex(IndexConfig{MaxMemoryBytes: 10 * 1024 * 1024})

	for i := 1; i <= 100; i++ {
		lvl := "INFO"
		if i%5 == 0 {
			lvl = "ERROR"
		}
		srv := "auth-api"
		if i%2 == 0 {
			srv = "payment-svc"
		}

		entry := parser.LogEntry{
			ID:        uint64(i),
			Timestamp: int64(1000 + i),
			Service:   srv,
			Level:     lvl,
			TraceID:   fmt.Sprintf("trace-%d", i),
			Message:   "test message",
			SizeBytes: 128,
		}
		idx.Insert(entry)
	}

	results := idx.Query(1000, 1100, "ERROR", "auth-api", "", 50, "asc")
	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Level != "ERROR" || r.Service != "auth-api" {
			t.Errorf("unexpected entry returned: %+v", r)
		}
	}
}

// Regression Test 1: Eviction/Insert Race and Memory Accounting Drift
func TestStoreIndex_EvictionInsertRace_AccountingDrift(t *testing.T) {
	// 50KB ceiling to trigger frequent evictions
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    50 * 1024,
		BucketDurationSec: 2,
	})

	var wg sync.WaitGroup
	numWriters := 8
	insertsPerWriter := 500

	baseTime := time.Now().Unix()

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < insertsPerWriter; i++ {
				// Timestamps span multiple partitions to force evictions
				ts := baseTime + int64((i%40)*2)
				id := uint64(workerID*10000 + i + 1)
				idx.Insert(parser.LogEntry{
					ID:        id,
					Timestamp: ts,
					Service:   "test-svc",
					Level:     "INFO",
					TraceID:   "tr",
					Message:   "log line payload",
					SizeBytes: 150,
				})
			}
		}(w)
	}

	wg.Wait()

	accounted := idx.CurrentMemoryUsage()
	live := idx.LivePartitionBytes()

	if accounted != live {
		t.Fatalf("memory accounting drift detected! Accounted: %d bytes, Sum of Live Partitions: %d bytes (diff: %d)",
			accounted, live, accounted-live)
	}

	if accounted > idx.maxMemoryBytes {
		t.Errorf("accounted memory %d exceeded ceiling %d", accounted, idx.maxMemoryBytes)
	}
}

// Regression Test 2: Never evict newest partition / single partition overflow
func TestStoreIndex_SinglePartitionOverflow(t *testing.T) {
	// 1000 bytes ceiling with 60s bucket
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    1000,
		BucketDurationSec: 60,
	})

	// Insert 20 entries into the exact same bucket (same timestamp)
	fixedTs := time.Now().Unix()
	for i := 1; i <= 20; i++ {
		idx.Insert(parser.LogEntry{
			ID:        uint64(i),
			Timestamp: fixedTs,
			Service:   "api",
			Level:     "INFO",
			TraceID:   "tr",
			Message:   "msg",
			SizeBytes: 200,
		})
	}

	// Partition count must still be 1 (never dropped wholesale)
	if idx.PartitionCount() != 1 {
		t.Fatalf("expected 1 live partition, got %d", idx.PartitionCount())
	}

	accounted := idx.CurrentMemoryUsage()
	live := idx.LivePartitionBytes()
	if accounted != live {
		t.Fatalf("memory accounting drift: accounted %d vs live %d", accounted, live)
	}

	if accounted > 1000 {
		t.Errorf("memory %d was not capped below ceiling 1000", accounted)
	}

	if idx.TotalRecords() >= 20 {
		t.Errorf("expected entries to be shed from single partition, but had %d records", idx.TotalRecords())
	}
}

// Regression Test 6: Oracle Test - 10k random entries, 1k random queries comparing Query against brute force slice
func TestStoreIndex_Oracle(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))

	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    100 * 1024 * 1024, // no eviction during oracle test
		BucketDurationSec: 10,
	})

	services := []string{"auth-api", "payment-svc", "order-router", "user-profile"}
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}

	const totalEntries = 10000
	bruteForceRecords := make([]parser.LogEntry, totalEntries)
	baseTime := int64(1730000000)

	for i := 0; i < totalEntries; i++ {
		entry := parser.LogEntry{
			ID:        uint64(i + 1),
			Timestamp: baseTime + int64(rnd.Intn(1000)), // spread over 1000 seconds
			Service:   services[rnd.Intn(len(services))],
			Level:     levels[rnd.Intn(len(levels))],
			TraceID:   fmt.Sprintf("trace-%d", i+1),
			Message:   fmt.Sprintf("payload message %d", i+1),
			SizeBytes: 128,
		}
		bruteForceRecords[i] = entry
		idx.Insert(entry)
	}

	// 1000 random queries
	const totalQueries = 1000
	orders := []string{"asc", "desc"}

	for q := 0; q < totalQueries; q++ {
		start := baseTime + int64(rnd.Intn(800))
		end := start + int64(rnd.Intn(300))
		order := orders[rnd.Intn(len(orders))]
		limit := rnd.Intn(50) + 1

		var filterService string
		if rnd.Float64() < 0.7 {
			filterService = services[rnd.Intn(len(services))]
		}
		var filterLevel string
		if rnd.Float64() < 0.7 {
			filterLevel = levels[rnd.Intn(len(levels))]
		}

		// Brute force search
		var expected []parser.LogEntry
		for _, r := range bruteForceRecords {
			if r.Timestamp < start || r.Timestamp > end {
				continue
			}
			if filterService != "" && r.Service != filterService {
				continue
			}
			if filterLevel != "" && r.Level != filterLevel {
				continue
			}
			expected = append(expected, r)
		}

		if order == "desc" {
			sort.Slice(expected, func(i, j int) bool {
				if expected[i].Timestamp == expected[j].Timestamp {
					return expected[i].ID > expected[j].ID
				}
				return expected[i].Timestamp > expected[j].Timestamp
			})
		} else {
			sort.Slice(expected, func(i, j int) bool {
				if expected[i].Timestamp == expected[j].Timestamp {
					return expected[i].ID < expected[j].ID
				}
				return expected[i].Timestamp < expected[j].Timestamp
			})
		}

		if len(expected) > limit {
			expected = expected[:limit]
		}

		actual := idx.Query(start, end, filterLevel, filterService, "", limit, order)

		if len(actual) != len(expected) {
			t.Fatalf("Query %d mismatch count: expected %d, got %d (start=%d, end=%d, level=%s, service=%s, limit=%d, order=%s)",
				q, len(expected), len(actual), start, end, filterLevel, filterService, limit, order)
		}

		for i := range actual {
			if actual[i].ID != expected[i].ID || actual[i].Timestamp != expected[i].Timestamp {
				t.Fatalf("Query %d item %d mismatch: expected ID %d (ts %d), got ID %d (ts %d)",
					q, i, expected[i].ID, expected[i].Timestamp, actual[i].ID, actual[i].Timestamp)
			}
		}
	}
}

func TestStoreIndex_Grep(t *testing.T) {
	idx := NewStoreIndex(IndexConfig{MaxMemoryBytes: 10 * 1024 * 1024})

	idx.Insert(parser.LogEntry{ID: 1, Timestamp: 100, Service: "api", Level: "INFO", Message: "User 42 logged in", SizeBytes: 120})
	idx.Insert(parser.LogEntry{ID: 2, Timestamp: 101, Service: "api", Level: "ERROR", Message: "Database connection failed", SizeBytes: 120})
	idx.Insert(parser.LogEntry{ID: 3, Timestamp: 102, Service: "api", Level: "WARN", Message: "Database connection slow", SizeBytes: 120})
	idx.Insert(parser.LogEntry{ID: 4, Timestamp: 103, Service: "worker", Level: "ERROR", Message: "Payment queue overflow", SizeBytes: 120})

	// Grep "database" (case-insensitive)
	results := idx.Query(0, 200, "", "", "database", 10, "asc")
	if len(results) != 2 {
		t.Fatalf("expected 2 matches for grep 'database', got %d", len(results))
	}
	if results[0].ID != 2 || results[1].ID != 3 {
		t.Errorf("unexpected results: %+v", results)
	}

	// Grep "database" with level "ERROR"
	results = idx.Query(0, 200, "ERROR", "", "database", 10, "asc")
	if len(results) != 1 || results[0].ID != 2 {
		t.Fatalf("expected 1 match for level ERROR + grep 'database', got %+v", results)
	}
}

func TestStoreIndex_Burn(t *testing.T) {
	idx := NewStoreIndex(IndexConfig{MaxMemoryBytes: 10 * 1024 * 1024})

	for i := 1; i <= 50; i++ {
		idx.Insert(parser.LogEntry{
			ID:        uint64(i),
			Timestamp: int64(1000 + i*10),
			Service:   "test",
			Level:     "INFO",
			Message:   "message",
			SizeBytes: 128,
		})
	}

	if idx.TotalRecords() != 50 {
		t.Fatalf("expected 50 total records before burn, got %d", idx.TotalRecords())
	}
	if idx.CurrentMemoryUsage() <= 0 {
		t.Fatalf("expected non-zero memory usage before burn, got %d", idx.CurrentMemoryUsage())
	}

	purged := idx.Burn()
	if purged != 50 {
		t.Errorf("expected 50 purged records, got %d", purged)
	}
	if idx.TotalRecords() != 0 {
		t.Errorf("expected 0 total records after burn, got %d", idx.TotalRecords())
	}
	if idx.CurrentMemoryUsage() != 0 {
		t.Errorf("expected 0 memory usage after burn, got %d", idx.CurrentMemoryUsage())
	}
	if idx.PartitionCount() != 0 {
		t.Errorf("expected 0 partitions after burn, got %d", idx.PartitionCount())
	}

	// Query after burn returns empty
	results := idx.Query(0, 2000, "", "", "", 10, "asc")
	if len(results) != 0 {
		t.Errorf("expected 0 query results after burn, got %d", len(results))
	}
}

func TestStoreIndex_SealedChunksOracle(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    256 * 1024 * 1024,
		BucketDurationSec: 5,
	})

	services := []string{"auth-api", "payment-svc", "order-router", "user-profile", "notification-gw"}
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}

	// 120,000 entries in two 5s buckets -> ~7,500/shard/bucket: seals one 4096-entry chunk per shard per bucket + leaves active chunks
	const totalEntries = 120000
	brute := make([]parser.LogEntry, totalEntries)
	baseTime := int64(1730000000)

	for i := 0; i < totalEntries; i++ {
		entry := parser.LogEntry{
			ID:        uint64(i + 1),
			Timestamp: baseTime + int64(rnd.Intn(10)),
			Service:   services[rnd.Intn(len(services))],
			Level:     levels[rnd.Intn(len(levels))],
			TraceID:   fmt.Sprintf("tr-%d", i+1),
			Message:   fmt.Sprintf("sealed oracle msg %d", i%17),
			SizeBytes: 128,
		}
		brute[i] = entry
		idx.Insert(entry)
	}

	orders := []string{"asc", "desc"}
	for q := 0; q < 200; q++ {
		start := baseTime + int64(rnd.Intn(8))
		end := start + int64(rnd.Intn(5))
		order := orders[rnd.Intn(len(orders))]
		limit := rnd.Intn(60) + 1

		var filterService, filterLevel, filterGrep string
		if rnd.Float64() < 0.7 {
			filterService = services[rnd.Intn(len(services))]
		}
		if rnd.Float64() < 0.7 {
			filterLevel = levels[rnd.Intn(len(levels))]
		}
		if rnd.Float64() < 0.25 {
			filterGrep = "msg 7"
		}

		var expected []parser.LogEntry
		for _, r := range brute {
			if r.Timestamp < start || r.Timestamp > end {
				continue
			}
			if filterService != "" && r.Service != filterService {
				continue
			}
			if filterLevel != "" && r.Level != filterLevel {
				continue
			}
			if filterGrep != "" && r.Message != "sealed oracle msg 7" {
				continue
			}
			expected = append(expected, r)
		}

		if order == "desc" {
			sort.Slice(expected, func(i, j int) bool {
				if expected[i].Timestamp == expected[j].Timestamp {
					return expected[i].ID > expected[j].ID
				}
				return expected[i].Timestamp > expected[j].Timestamp
			})
		} else {
			sort.Slice(expected, func(i, j int) bool {
				if expected[i].Timestamp == expected[j].Timestamp {
					return expected[i].ID < expected[j].ID
				}
				return expected[i].Timestamp < expected[j].Timestamp
			})
		}
		if len(expected) > limit {
			expected = expected[:limit]
		}

		actual := idx.Query(start, end, filterLevel, filterService, filterGrep, limit, order)
		if len(actual) != len(expected) {
			t.Fatalf("SealedOracle query %d count mismatch: expected %d, got %d", q, len(expected), len(actual))
		}
		for i := range actual {
			if actual[i].ID != expected[i].ID || actual[i].Timestamp != expected[i].Timestamp {
				t.Fatalf("SealedOracle query %d item %d mismatch: expected (%d,%d), got (%d,%d)",
					q, i, expected[i].Timestamp, expected[i].ID, actual[i].Timestamp, actual[i].ID)
			}
		}
	}
}

func TestStoreIndex_ConcurrentSealNoMissedChunk(t *testing.T) {
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    256 * 1024 * 1024,
		BucketDurationSec: 60,
	})

	const baseTs = int64(1730000100)
	const totalPerShard = 9000 // Seals two 4096-entry chunks on shard 1 and leaves 808 active

	done := make(chan struct{})
	errCh := make(chan error, 8)
	var wg sync.WaitGroup

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				res := idx.Query(baseTs, baseTs, "INFO", "auth-api", "", MaxQueryLimit, "asc")
				for j, item := range res {
					wantID := uint64(1 + j*NumPartitionShards)
					if item.ID != wantID {
						select {
						case errCh <- fmt.Errorf("gap or duplicate during chunk seal at index %d: got ID %d, want %d (len=%d)", j, item.ID, wantID, len(res)):
						default:
						}
						return
					}
				}
			}
		}()
	}

	for i := 0; i < totalPerShard; i++ {
		idx.Insert(parser.LogEntry{
			ID:        uint64(1 + i*NumPartitionShards),
			Timestamp: baseTs,
			Service:   "auth-api",
			Level:     "INFO",
			TraceID:   "tr-seal",
			Message:   "seal transition check",
			SizeBytes: 128,
		})
	}
	close(done)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}

	final := idx.Query(baseTs, baseTs, "INFO", "auth-api", "", MaxQueryLimit, "asc")
	if len(final) != totalPerShard {
		t.Fatalf("expected %d final entries, got %d", totalPerShard, len(final))
	}
}

func TestStoreIndex_ConcurrentEvictionAndBurnAccountingNoDrift(t *testing.T) {
	// Case 1: Multi-bucket + sealed-chunk eviction + concurrent Burn()
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    2 * 1024 * 1024,
		BucketDurationSec: 5,
	})

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 12000; i++ {
				id := uint64(worker*12000 + i + 1)
				idx.Insert(parser.LogEntry{
					ID:        id,
					Timestamp: 1730000000 + int64(i/400),
					Service:   "auth-api",
					Level:     "INFO",
					TraceID:   "tr-acct",
					Message:   "accounting check",
					SizeBytes: 512,
				})
				if worker == 0 && i == 6000 {
					idx.Burn()
				}
			}
		}(w)
	}
	wg.Wait()

	if live, cur := idx.LivePartitionBytes(), idx.CurrentMemoryUsage(); live != cur {
		t.Fatalf("multi-bucket accounting drift: LivePartitionBytes=%d, CurrentMemoryUsage=%d", live, cur)
	}
	idx.Burn()
	if cur := idx.CurrentMemoryUsage(); cur != 0 {
		t.Fatalf("expected CurrentMemoryUsage=0 after Burn(), got %d", cur)
	}

	// Case 2: Tiny single-bucket tombstone fallback before any 4096-entry chunk seals
	tiny := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    32 * 1024,
		BucketDurationSec: 3600,
	})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := uint64(worker*500 + i + 1)
				tiny.Insert(parser.LogEntry{
					ID:        id,
					Timestamp: 1730000000 + int64(i%10),
					Service:   "payment-svc",
					Level:     "WARN",
					TraceID:   "tr-tiny",
					Message:   "tiny fallback check",
					SizeBytes: 512,
				})
			}
		}(w)
	}
	wg.Wait()

	if live, cur := tiny.LivePartitionBytes(), tiny.CurrentMemoryUsage(); live != cur {
		t.Fatalf("tiny-bucket accounting drift: LivePartitionBytes=%d, CurrentMemoryUsage=%d", live, cur)
	}
	tiny.Burn()
	if cur := tiny.CurrentMemoryUsage(); cur != 0 {
		t.Fatalf("expected tiny CurrentMemoryUsage=0 after Burn(), got %d", cur)
	}
}
