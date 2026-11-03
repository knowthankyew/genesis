package bench

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"project-genesis/internal/engine"
)

func TestGenesisBenchmarks(t *testing.T) {
	services := []string{"auth-api", "payment-svc", "order-router", "user-profile", "notification"}
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}

	// 1. Initialize Engine with 128MB ceiling to also test eviction stability
	eng := engine.NewEngine(engine.EngineConfig{
		MaxMemoryBytes:   128 * 1024 * 1024,
		QueueCapacity:    250000,
		PriorityQueueCap: 50000,
	})
	defer eng.Close()

	numClients := 50
	targetDuration := 5 * time.Second

	var totalIngested atomic.Uint64
	var totalQueries atomic.Uint64

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	t.Logf("=== GATE 1 & 2: LAUNCHING %d CONCURRENT CLIENT SIMULATORS ===", numClients)

	baseTime := time.Now().Unix()

	// Launch 50 concurrent ingestion workers
	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(clientID)))
			lineBuf := make([]byte, 0, 256)

			for {
				select {
				case <-stopCh:
					return
				default:
					srv := services[r.Intn(len(services))]
					lvl := levels[r.Intn(len(levels))]
					ts := baseTime + int64(r.Intn(3600))
					traceID := fmt.Sprintf("tr-%d-%06d", clientID, r.Intn(100000))

					lineBuf = lineBuf[:0]
					lineBuf = fmt.Appendf(lineBuf, "%d|%s|%s|%s|Simulated payload message for client %d\n",
						ts, srv, lvl, traceID, clientID)

					if err := eng.Ingest(lineBuf); err == nil {
						totalIngested.Add(1)
					}
				}
			}
		}(c)
	}

	// Concurrent Query Latency Collector
	numQueryClients := 5
	var queryLatenciesMu sync.Mutex
	var queryLatencies []time.Duration

	for q := 0; q < numQueryClients; q++ {
		wg.Add(1)
		go func(qID int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(qID*999)))

			for {
				select {
				case <-stopCh:
					return
				default:
					startWindow := baseTime + int64(r.Intn(1800))
					endWindow := startWindow + int64(r.Intn(600))
					srv := services[r.Intn(len(services))]
					lvl := levels[r.Intn(len(levels))]

					qStart := time.Now()
					_ = eng.QueryDirect(startWindow, endWindow, lvl, srv, 50)
					duration := time.Since(qStart)

					queryLatenciesMu.Lock()
					queryLatencies = append(queryLatencies, duration)
					queryLatenciesMu.Unlock()

					totalQueries.Add(1)
					time.Sleep(5 * time.Millisecond) // query frequency
				}
			}
		}(q)
	}

	// Run benchmark for target duration
	time.Sleep(targetDuration)
	close(stopCh)
	wg.Wait()

	// Wait for queue drain
	time.Sleep(200 * time.Millisecond)

	elapsed := targetDuration.Seconds()
	ingested := totalIngested.Load()
	rate := float64(ingested) / elapsed

	t.Logf("----------------------------------------------------------")
	t.Logf("BENCHMARK RESULTS")
	t.Logf("----------------------------------------------------------")
	t.Logf("Duration:          %.2f s", elapsed)
	t.Logf("Ingestion Total:   %d logs", ingested)
	t.Logf("Throughput:        %.2f logs/sec", rate)
	t.Logf("Total Queries Run: %d queries", totalQueries.Load())

	// GATE 1: Throughput >= 50,000 lines/sec
	if rate < 50000 {
		t.Errorf("FAIL GATE 1: Expected throughput >= 50,000 logs/sec, got %.2f logs/sec", rate)
	} else {
		t.Logf("PASS GATE 1: Throughput = %.2f logs/sec (Target: >= 50,000)", rate)
	}

	// GATE 2: P99 Query Latency < 5ms
	queryLatenciesMu.Lock()
	sort.Slice(queryLatencies, func(i, j int) bool {
		return queryLatencies[i] < queryLatencies[j]
	})

	if len(queryLatencies) > 0 {
		p50 := queryLatencies[len(queryLatencies)*50/100]
		p90 := queryLatencies[len(queryLatencies)*90/100]
		p99 := queryLatencies[len(queryLatencies)*99/100]

		t.Logf("Query Latency p50: %v", p50)
		t.Logf("Query Latency p90: %v", p90)
		t.Logf("Query Latency p99: %v", p99)

		if p99 > 5*time.Millisecond {
			t.Errorf("FAIL GATE 2: Expected p99 < 5ms, got %v", p99)
		} else {
			t.Logf("PASS GATE 2: P99 Latency = %v (Target: < 5ms)", p99)
		}
	}
	queryLatenciesMu.Unlock()
}
