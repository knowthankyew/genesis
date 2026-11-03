package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/knowthankyew/genesis/internal/index"
	"github.com/knowthankyew/genesis/internal/parser"
)

var (
	ErrBackpressure = errors.New("pipeline backpressure: queue saturated")
)

// EngineConfig holds tuning options for the ingestion & query engine.
type EngineConfig struct {
	MaxMemoryBytes  int64
	QueueCapacity   int
	PriorityQueueCap int
	NumWorkers      int
}

// Stats tracks pipeline throughput, memory, and latency metrics.
type Stats struct {
	IngestedCount   uint64 `json:"ingested_count"`
	PriorityCount   uint64 `json:"priority_count"`
	DroppedCount    uint64 `json:"dropped_count"`
	CurrentMemory   int64  `json:"current_memory_bytes"`
	ActiveRecords   int    `json:"active_records"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
}

// Engine coordinates the worker parser pool, backpressure channels, index, and query API.
type Engine struct {
	index       *index.StoreIndex
	normalCh    chan []byte
	priorityCh  chan []byte
	numWorkers  int
	idCounter   atomic.Uint64
	startTime   time.Time

	// Metrics
	ingestedCount atomic.Uint64
	priorityCount atomic.Uint64
	droppedCount  atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewEngine initializes the core Genesis engine.
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 100000
	}
	if cfg.PriorityQueueCap <= 0 {
		cfg.PriorityQueueCap = 25000
	}
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = runtime.NumCPU() * 2
	}

	idx := index.NewStoreIndex(index.IndexConfig{
		MaxMemoryBytes: cfg.MaxMemoryBytes,
	})

	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		index:       idx,
		normalCh:    make(chan []byte, cfg.QueueCapacity),
		priorityCh:  make(chan []byte, cfg.PriorityQueueCap),
		numWorkers:  cfg.NumWorkers,
		startTime:   time.Now(),
		ctx:         ctx,
		cancel:      cancel,
	}

	e.startWorkers()
	return e
}

func (e *Engine) startWorkers() {
	for i := 0; i < e.numWorkers; i++ {
		e.wg.Add(1)
		go e.workerLoop()
	}
}

// workerLoop drains priority channel first, then normal channel.
func (e *Engine) workerLoop() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case raw := <-e.priorityCh:
			e.processRawLog(raw)
		default:
			select {
			case <-e.ctx.Done():
				return
			case raw := <-e.priorityCh:
				e.processRawLog(raw)
			case raw := <-e.normalCh:
				e.processRawLog(raw)
			}
		}
	}
}

func (e *Engine) processRawLog(raw []byte) {
	id := e.idCounter.Add(1)
	entry, err := parser.ParseLogLine(id, raw)
	if err != nil {
		return
	}
	e.index.Insert(entry)
	e.ingestedCount.Add(1)
}

// Ingest handles ingestion of raw log bytes with backpressure management.
// Critical error logs are prioritized and will never be dropped.
func (e *Engine) Ingest(raw []byte) error {
	// Defensive copy if buffer is reused by socket reader
	payload := make([]byte, len(raw))
	copy(payload, raw)

	isCritical := bytes.Contains(payload, []byte("|ERROR|")) ||
		bytes.Contains(payload, []byte("|FATAL|"))

	if isCritical {
		// Priority channel with blocking delivery to guarantee no error logs are dropped
		select {
		case e.priorityCh <- payload:
			e.priorityCount.Add(1)
			return nil
		case <-e.ctx.Done():
			return errors.New("engine stopped")
		}
	}

	// Normal channel: try non-blocking push
	select {
	case e.normalCh <- payload:
		return nil
	default:
		// Queue full: apply backpressure or graceful drop
		e.droppedCount.Add(1)
		return ErrBackpressure
	}
}

// QueryRequest parameters
type QueryResponse struct {
	Count     int               `json:"count"`
	LatencyUs int64             `json:"latency_us"`
	Logs      []parser.LogEntry `json:"logs"`
}

// HandleQuery processes GET /query?start=1730000000&end=1730003600&level=ERROR&service=auth-api
func (e *Engine) HandleQuery(w http.ResponseWriter, r *http.Request) {
	startTs := time.Now()

	query := r.URL.Query()
	startStr := query.Get("start")
	endStr := query.Get("end")
	level := query.Get("level")
	service := query.Get("service")
	limitStr := query.Get("limit")

	var start, end int64
	if startStr != "" {
		start, _ = strconv.ParseInt(startStr, 10, 64)
	}
	if endStr != "" {
		end, _ = strconv.ParseInt(endStr, 10, 64)
	} else {
		end = 1<<62 - 1 // max time
	}

	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	entries := e.index.Query(start, end, level, service, limit)
	latency := time.Since(startTs).Microseconds()

	resp := QueryResponse{
		Count:     len(entries),
		LatencyUs: latency,
		Logs:      entries,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleStats returns runtime metrics.
func (e *Engine) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := Stats{
		IngestedCount: e.ingestedCount.Load(),
		PriorityCount: e.priorityCount.Load(),
		DroppedCount:  e.droppedCount.Load(),
		CurrentMemory: e.index.CurrentMemoryUsage(),
		ActiveRecords: e.index.TotalRecords(),
		UptimeSeconds: int64(time.Since(e.startTime).Seconds()),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

// QueryDirect allows in-process querying for benchmark tests.
func (e *Engine) QueryDirect(start, end int64, level, service string, limit int) []parser.LogEntry {
	return e.index.Query(start, end, level, service, limit)
}

// Close gracefully stops the engine.
func (e *Engine) Close() {
	e.cancel()
	e.wg.Wait()
}
