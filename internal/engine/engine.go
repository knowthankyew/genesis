package engine

import (
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
	ErrStopped      = errors.New("engine is stopped or closing")
)

type EngineConfig struct {
	MaxMemoryBytes   int64
	QueueCapacity    int
	PriorityQueueCap int
	NumWorkers       int
	MaxLineLength    int
	BucketDuration   int64
}

type DroppedStats struct {
	QueueFull atomic.Uint64 `json:"queue_full"`
	Malformed atomic.Uint64 `json:"malformed"`
	Stopped   atomic.Uint64 `json:"stopped"`
}

type Stats struct {
	ReceivedCount  uint64         `json:"received"`
	IndexedCount   uint64         `json:"indexed"`
	PriorityCount  uint64         `json:"priority"`
	Dropped        map[string]uint64 `json:"dropped"`
	Evicted        uint64         `json:"evicted"`
	Partitions     int            `json:"partitions"`
	AccountedBytes int64          `json:"accounted_bytes"`
	HeapBytes      uint64         `json:"heap_bytes"`
	QueueDepths    map[string]int `json:"queue_depths"`
	UptimeSeconds  int64          `json:"uptime_seconds"`
}

type Engine struct {
	index         *index.StoreIndex
	normalCh      chan []byte
	priorityCh    chan []byte
	numWorkers    int
	maxLineLength int
	idCounter     atomic.Uint64
	startTime     time.Time
	closing       atomic.Bool

	// Metrics
	receivedCount atomic.Uint64
	indexedCount  atomic.Uint64
	priorityCount atomic.Uint64
	dropped       DroppedStats

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewEngine(cfg EngineConfig) *Engine {
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 200000
	}
	if cfg.PriorityQueueCap <= 0 {
		cfg.PriorityQueueCap = 50000
	}
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = runtime.NumCPU() * 2
	}
	if cfg.MaxLineLength <= 0 {
		cfg.MaxLineLength = parser.DefaultMaxLineLength
	}

	bucketDuration := cfg.BucketDuration
	if bucketDuration <= 0 {
		bucketDuration = 5 // default 5s partitions
	}

	idx := index.NewStoreIndex(index.IndexConfig{
		MaxMemoryBytes:    cfg.MaxMemoryBytes,
		BucketDurationSec: bucketDuration,
	})

	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		index:         idx,
		normalCh:      make(chan []byte, cfg.QueueCapacity),
		priorityCh:    make(chan []byte, cfg.PriorityQueueCap),
		numWorkers:    cfg.NumWorkers,
		maxLineLength: cfg.MaxLineLength,
		startTime:     time.Now(),
		ctx:           ctx,
		cancel:        cancel,
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

func (e *Engine) workerLoop() {
	defer e.wg.Done()

	for {
		select {
		case raw := <-e.priorityCh:
			e.processRawLog(raw)
		default:
			select {
			case <-e.ctx.Done():
				// Drain remaining priority items before exiting
				for {
					select {
					case raw := <-e.priorityCh:
						e.processRawLog(raw)
					default:
						return
					}
				}
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
	entry, err := parser.ParseLogLine(id, raw, e.maxLineLength)
	if err != nil {
		e.dropped.Malformed.Add(1)
		return
	}
	e.index.Insert(entry)
	e.indexedCount.Add(1)
}

// Ingest routes logs into priority or normal lanes.
// Priority routing is based on the PARSED log level (not substring).
func (e *Engine) Ingest(raw []byte) error {
	if e.closing.Load() {
		e.dropped.Stopped.Add(1)
		return ErrStopped
	}

	e.receivedCount.Add(1)

	payload := make([]byte, len(raw))
	copy(payload, raw)

	// Route based on parsed log level (Fix #3)
	level := parser.ExtractLevel(payload)
	isCritical := level == "ERROR" || level == "FATAL"

	if isCritical {
		select {
		case e.priorityCh <- payload:
			e.priorityCount.Add(1)
			return nil
		case <-e.ctx.Done():
			e.dropped.Stopped.Add(1)
			return ErrStopped
		}
	}

	// Normal lane
	select {
	case e.normalCh <- payload:
		return nil
	default:
		e.dropped.QueueFull.Add(1)
		return ErrBackpressure
	}
}

type QueryResponse struct {
	Count     int               `json:"count"`
	LatencyUs int64             `json:"latency_us"`
	Logs      []parser.LogEntry `json:"logs"`
}

// HandleQuery processes GET /query with input validation (Fix #7)
func (e *Engine) HandleQuery(w http.ResponseWriter, r *http.Request) {
	startTs := time.Now()

	query := r.URL.Query()
	startStr := query.Get("start")
	endStr := query.Get("end")
	level := query.Get("level")
	service := query.Get("service")
	limitStr := query.Get("limit")
	order := query.Get("order")

	var start, end int64
	var err error

	if startStr != "" {
		start, err = strconv.ParseInt(startStr, 10, 64)
		if err != nil || start < 0 {
			http.Error(w, `{"error":"invalid 'start' parameter"}`, http.StatusBadRequest)
			return
		}
	}

	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < 0 {
			http.Error(w, `{"error":"invalid 'end' parameter"}`, http.StatusBadRequest)
			return
		}
	} else {
		end = 1<<62 - 1
	}

	if end < start {
		http.Error(w, `{"error":"'end' cannot be less than 'start'"}`, http.StatusBadRequest)
		return
	}

	limit := 100
	if limitStr != "" {
		l, err := strconv.Atoi(limitStr)
		if err != nil || l <= 0 {
			http.Error(w, `{"error":"invalid 'limit' parameter"}`, http.StatusBadRequest)
			return
		}
		if l > 10000 {
			l = 10000
		}
		limit = l
	}

	if order != "desc" {
		order = "asc"
	}

	entries := e.index.Query(start, end, level, service, limit, order)
	latency := time.Since(startTs).Microseconds()

	resp := QueryResponse{
		Count:     len(entries),
		LatencyUs: latency,
		Logs:      entries,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleStats returns full pipeline statistics (Fix #10)
func (e *Engine) HandleStats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	stats := Stats{
		ReceivedCount:  e.receivedCount.Load(),
		IndexedCount:   e.indexedCount.Load(),
		PriorityCount:  e.priorityCount.Load(),
		Dropped: map[string]uint64{
			"queue_full": e.dropped.QueueFull.Load(),
			"malformed":  e.dropped.Malformed.Load(),
			"stopped":    e.dropped.Stopped.Load(),
		},
		Evicted:        e.index.EvictedCount(),
		Partitions:     e.index.PartitionCount(),
		AccountedBytes: e.index.CurrentMemoryUsage(),
		HeapBytes:      m.HeapAlloc,
		QueueDepths: map[string]int{
			"normal":   len(e.normalCh),
			"priority": len(e.priorityCh),
		},
		UptimeSeconds:  int64(time.Since(e.startTime).Seconds()),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stats)
}

func (e *Engine) QueryDirect(start, end int64, level, service string, limit int, order string) []parser.LogEntry {
	return e.index.Query(start, end, level, service, limit, order)
}

func (e *Engine) IndexedCount() uint64 {
	return e.indexedCount.Load()
}

func (e *Engine) CurrentMemoryUsage() int64 {
	return e.index.CurrentMemoryUsage()
}

// Close gracefully stops the engine and guarantees draining priority queue (Fix #4)
func (e *Engine) Close() {
	e.closing.Store(true)

	// Wait up to 3s for priority channel to drain
	deadline := time.Now().Add(3 * time.Second)
	for len(e.priorityCh) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	e.cancel()
	e.wg.Wait()
}
