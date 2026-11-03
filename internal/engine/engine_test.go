package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/knowthankyew/genesis/internal/config"
	"github.com/knowthankyew/genesis/internal/storage"
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

func TestEngine_Burn_Endpoint(t *testing.T) {
	eng := NewEngine(EngineConfig{})
	defer eng.Close()

	for i := 0; i < 20; i++ {
		_ = eng.Ingest([]byte(fmt.Sprintf("1730000000|auth-api|INFO|tr-%d|Log payload %d\n", i, i)))
	}
	time.Sleep(50 * time.Millisecond)

	req := httptest.NewRequest("DELETE", "/logs", nil)
	w := httptest.NewRecorder()
	eng.HandleBurn(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode burn response: %v", err)
	}

	if resp["status"] != "burned" {
		t.Errorf("expected status 'burned', got %v", resp["status"])
	}
	if purged, ok := resp["purged"].(float64); !ok || int(purged) != 20 {
		t.Errorf("expected 20 purged records, got %v", resp["purged"])
	}
	if eng.CurrentMemoryUsage() != 0 {
		t.Errorf("expected 0 current memory usage after burn, got %d", eng.CurrentMemoryUsage())
	}
}

func TestEngine_MultiFormat_Ingest(t *testing.T) {
	eng := NewEngine(EngineConfig{})
	defer eng.Close()

	// 1. Syslog RFC 5424
	_ = eng.Ingest([]byte("<165>1 2026-11-02T22:14:15Z host order-svc 1234 tr-sys - Order created\n"))
	// 2. JSON Lines
	_ = eng.Ingest([]byte(`{"service":"payment-svc","level":"error","msg":"Upstream gateway timeout"}` + "\n"))
	// 3. Plain text
	_ = eng.Ingest([]byte("Fatal: Out of file descriptors\n"))

	time.Sleep(50 * time.Millisecond)

	// Check /query for each
	req := httptest.NewRequest("GET", "/query?grep=gateway", nil)
	w := httptest.NewRecorder()
	eng.HandleQuery(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var qResp QueryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &qResp); err != nil {
		t.Fatalf("failed to parse query response: %v", err)
	}

	if len(qResp.Logs) != 1 {
		t.Fatalf("expected 1 log matching grep 'gateway', got %d", len(qResp.Logs))
	}
	if qResp.Logs[0].Service != "payment-svc" {
		t.Errorf("expected service 'payment-svc', got %q", qResp.Logs[0].Service)
	}
}

func TestEngine_PersistenceReplay_NoDeadlockOrDuplication(t *testing.T) {
	tmpDir := t.TempDir()

	// Run 1: Open DiskStore + Engine, ingest 10 lines, close
	ds1, err := storage.Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open disk store 1: %v", err)
	}
	eng1 := NewEngine(EngineConfig{Store: ds1})
	for i := 0; i < 10; i++ {
		_ = eng1.Ingest([]byte(fmt.Sprintf("173000000%d|auth-api|INFO|tr-%d|Persistent line %d\n", i, i, i)))
	}
	for eng1.IndexedCount() < 10 {
		time.Sleep(10 * time.Millisecond)
	}
	eng1.Close()
	_ = ds1.Close()

	// Run 2: Reopen DiskStore + Engine, replay via IngestReplay (must not deadlock or duplicate on disk)
	ds2, err := storage.Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open disk store 2: %v", err)
	}
	eng2 := NewEngine(EngineConfig{Store: ds2})
	if err := ds2.Replay(func(raw []byte) {
		_ = eng2.IngestReplay(raw)
	}); err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	for eng2.IndexedCount() < 10 {
		time.Sleep(10 * time.Millisecond)
	}

	// Verify trace query parameter
	reqTrace := httptest.NewRequest("GET", "/query?trace=tr-5", nil)
	wTrace := httptest.NewRecorder()
	eng2.HandleQuery(wTrace, reqTrace)
	if wTrace.Code != http.StatusOK {
		t.Fatalf("expected 200 on trace query, got %d", wTrace.Code)
	}
	var qResp QueryResponse
	_ = json.Unmarshal(wTrace.Body.Bytes(), &qResp)
	if len(qResp.Logs) != 1 || qResp.Logs[0].TraceID != "tr-5" {
		t.Fatalf("expected 1 log with trace_id 'tr-5', got %+v", qResp.Logs)
	}

	// Verify POST /logs is rejected (CSRF protection: only DELETE allowed)
	reqPostBurn := httptest.NewRequest("POST", "/logs", nil)
	wPostBurn := httptest.NewRecorder()
	eng2.HandleBurn(wPostBurn, reqPostBurn)
	if wPostBurn.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed for POST /logs, got %d", wPostBurn.Code)
	}

	eng2.Close()
	_ = ds2.Close()

	// Run 3: Reopen DiskStore again and verify total replayed entries is still 10 (not 20)
	ds3, err := storage.Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open disk store 3: %v", err)
	}
	defer ds3.Close()
	var count int
	_ = ds3.Replay(func(raw []byte) {
		count++
	})
	if count != 10 {
		t.Fatalf("expected exactly 10 entries on disk after restart replay, got %d (duplication bug!)", count)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	writeDeadlineSet bool
	writeDeadlineVal time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.writeDeadlineSet = true
	d.writeDeadlineVal = t
	return nil
}

func TestSecurityInvariants_LoopbackAndBurnAndCORS(t *testing.T) {
	cfg := config.DefaultConfig()
	for name, addr := range map[string]string{
		"TCPAddr":    cfg.TCPAddr,
		"UDPAddr":    cfg.UDPAddr,
		"SyslogAddr": cfg.SyslogAddr,
		"HTTPAddr":   cfg.HTTPAddr,
	} {
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Fatalf("security invariant violated: default %s=%q does not bind to 127.0.0.1", name, addr)
		}
	}

	eng := NewEngine(EngineConfig{})
	defer eng.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		req := httptest.NewRequest(method, "/logs", nil)
		w := httptest.NewRecorder()
		eng.HandleBurn(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 for %s /logs, got %d", method, w.Code)
		}
	}

	reqDel := httptest.NewRequest(http.MethodDelete, "/logs", nil)
	wDel := httptest.NewRecorder()
	eng.HandleBurn(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("expected 200 for DELETE /logs, got %d", wDel.Code)
	}

	reqQuery := httptest.NewRequest(http.MethodGet, "/query", nil)
	wQuery := httptest.NewRecorder()
	eng.HandleQuery(wQuery, reqQuery)
	if cors := wQuery.Header().Get("Access-Control-Allow-Origin"); cors != "" {
		t.Fatalf("unexpected CORS header on /query: %q", cors)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reqTail := httptest.NewRequest(http.MethodGet, "/tail", nil).WithContext(ctx)
	wTail := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	eng.HandleTail(wTail, reqTail)
	if !wTail.writeDeadlineSet || !wTail.writeDeadlineVal.IsZero() {
		t.Fatalf("expected SSE HandleTail to clear write deadline to zero time, got set=%v val=%v", wTail.writeDeadlineSet, wTail.writeDeadlineVal)
	}
	if ct := wTail.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream Content-Type on /tail, got %q", ct)
	}
	if cors := wTail.Header().Get("Access-Control-Allow-Origin"); cors != "" {
		t.Fatalf("unexpected CORS header on /tail: %q", cors)
	}
}
