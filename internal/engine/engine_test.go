package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// Regression Test 3: Priority lane routes by parsed level, not by substring
func TestEngine_PriorityRouting(t *testing.T) {
	eng := NewEngine(EngineConfig{
		QueueCapacity:    1000,
		PriorityQueueCap: 1000,
	})
	defer eng.Close()

	// Info line containing "|ERROR|" in message text
	infoLine := []byte("1730000000|auth-api|INFO|tr-1|User searched for keyword |ERROR| in database\n")
	_ = eng.Ingest(infoLine)

	// Real error line
	errLine := []byte("1730000000|auth-api|ERROR|tr-2|Critical failure in database\n")
	_ = eng.Ingest(errLine)

	if eng.priorityCount.Load() != 1 {
		t.Fatalf("expected priorityCount=1, got %d", eng.priorityCount.Load())
	}
}

// Regression Test 4: Close() drains priority channel
func TestEngine_CloseDrainsPriorityChannel(t *testing.T) {
	// 1 worker, large capacity
	eng := NewEngine(EngineConfig{
		QueueCapacity:    5000,
		PriorityQueueCap: 5000,
		NumWorkers:       1,
	})

	const n = 500
	for i := 0; i < n; i++ {
		line := fmt.Sprintf("1730000000|auth-api|ERROR|tr-%d|Error line %d\n", i, i)
		_ = eng.Ingest([]byte(line))
	}

	eng.Close()

	indexed := eng.IndexedCount()
	if indexed != n {
		t.Fatalf("expected all %d priority lines to be indexed before close, got %d", n, indexed)
	}
}

// Regression Test 7: Input validation on /query
func TestEngine_HandleQuery_Validation(t *testing.T) {
	eng := NewEngine(EngineConfig{})
	defer eng.Close()

	tests := []struct {
		name       string
		queryURL   string
		expectCode int
	}{
		{"valid query", "/query?start=1730000000&end=1730003600&level=ERROR&service=auth-api", http.StatusOK},
		{"bad start format", "/query?start=abc", http.StatusBadRequest},
		{"bad end format", "/query?end=xyz", http.StatusBadRequest},
		{"end before start", "/query?start=1730005000&end=1730001000", http.StatusBadRequest},
		{"bad limit format", "/query?limit=-5", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.queryURL, nil)
			w := httptest.NewRecorder()
			eng.HandleQuery(w, req)
			if w.Code != tc.expectCode {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.expectCode, w.Code, w.Body.String())
			}
		})
	}
}

// Regression Test 10: GET /stats endpoint
func TestEngine_HandleStats(t *testing.T) {
	eng := NewEngine(EngineConfig{})
	defer eng.Close()

	_ = eng.Ingest([]byte("1730000000|auth-api|ERROR|tr-1|Database error\n"))
	time.Sleep(50 * time.Millisecond) // wait for worker

	req := httptest.NewRequest("GET", "/stats", nil)
	w := httptest.NewRecorder()
	eng.HandleStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var stats Stats
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("failed to decode stats JSON: %v", err)
	}

	if stats.ReceivedCount == 0 {
		t.Errorf("expected received > 0")
	}
	if stats.IndexedCount == 0 {
		t.Errorf("expected indexed > 0")
	}
	if stats.AccountedBytes == 0 {
		t.Errorf("expected accounted_bytes > 0")
	}
	if stats.QueueDepths == nil {
		t.Errorf("expected queue_depths to be present")
	}
}

// Regression Test 9: Memory calibration test (insert 200k entries, estimate within ±25% of heap delta)
func TestEngine_MemoryCalibration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory calibration in short mode")
	}

	// Run GC before starting
	runtime.GC()
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	eng := NewEngine(EngineConfig{
		MaxMemoryBytes:   500 * 1024 * 1024,
		QueueCapacity:    250000,
		PriorityQueueCap: 50000,
		NumWorkers:       4,
	})

	const count = 200000
	baseTime := time.Now().Unix()

	for i := 0; i < count; i++ {
		line := fmt.Sprintf("%d|auth-api|INFO|tr-%08d|Standard log payload message for user authentication %d\n",
			baseTime+(int64(i%60)), i, i)
		_ = eng.Ingest([]byte(line))
	}

	// Wait for workers to index all
	for eng.IndexedCount() < count {
		time.Sleep(10 * time.Millisecond)
	}

	runtime.GC()
	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	heapDelta := int64(mAfter.HeapAlloc) - int64(mBefore.HeapAlloc)
	accounted := eng.CurrentMemoryUsage()

	eng.Close()

	if heapDelta <= 0 {
		t.Skipf("heap delta non-positive (%d), skipping ratio check", heapDelta)
	}

	ratio := float64(accounted) / float64(heapDelta)
	t.Logf("Memory Calibration: Accounted = %d bytes, HeapAlloc Delta = %d bytes, Ratio = %.2f",
		accounted, heapDelta, ratio)

	// Within ±25%: ratio between 0.75 and 1.25
	if ratio < 0.75 || ratio > 1.25 {
		t.Errorf("memory calibration outside ±25%% tolerance: ratio = %.2f (accounted=%d, heapDelta=%d)",
			ratio, accounted, heapDelta)
	}
}
