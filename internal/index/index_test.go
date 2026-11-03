package index

import (
	"fmt"
	"sync"
	"testing"

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

	// Query: auth-api with ERROR between 1000 and 1100
	results := idx.Query(1000, 1100, "ERROR", "auth-api", 50)
	// Expected: i % 5 == 0 and i % 2 != 0 -> 5, 15, 25, 35, 45, 55, 65, 75, 85, 95 (10 items)
	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Level != "ERROR" || r.Service != "auth-api" {
			t.Errorf("unexpected entry returned: %+v", r)
		}
	}
}

func TestStoreIndex_Eviction(t *testing.T) {
	// Set small limit: 1000 bytes (~5 entries of 200 bytes) with 1-second partitions
	idx := NewStoreIndex(IndexConfig{
		MaxMemoryBytes:    1000,
		BucketDurationSec: 1,
	})

	for i := 1; i <= 20; i++ {
		entry := parser.LogEntry{
			ID:        uint64(i),
			Timestamp: int64(i),
			Service:   "test-svc",
			Level:     "INFO",
			TraceID:   "tr",
			Message:   "msg",
			SizeBytes: 200,
		}
		idx.Insert(entry)
	}

	if idx.CurrentMemoryUsage() > 1000 {
		t.Errorf("memory usage %d exceeded ceiling 1000", idx.CurrentMemoryUsage())
	}
}

func TestStoreIndex_ConcurrentReadWrite(t *testing.T) {
	idx := NewStoreIndex(IndexConfig{MaxMemoryBytes: 50 * 1024 * 1024})
	var wg sync.WaitGroup

	// Writers
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := uint64(workerID*10000 + i)
				idx.Insert(parser.LogEntry{
					ID:        id,
					Timestamp: int64(1000 + (i % 50)),
					Service:   "auth-api",
					Level:     "ERROR",
					TraceID:   "tr",
					Message:   "log msg",
					SizeBytes: 100,
				})
			}
		}(w)
	}

	// Concurrent Readers
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = idx.Query(1010, 1030, "ERROR", "auth-api", 50)
			}
		}()
	}

	wg.Wait()
}
