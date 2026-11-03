package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/knowthankyew/genesis/internal/engine"
)

func RunStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	httpURL := fs.String("http", "http://127.0.0.1:8080", "Genesis HTTP address")
	asJSON := fs.Bool("json", false, "Output raw JSON")
	_ = fs.Parse(args)

	baseURL := strings.TrimRight(*httpURL, "/")
	resp, err := http.Get(baseURL + "/stats")
	if err != nil {
		return fmt.Errorf("failed to reach Genesis stats at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stats error (status %d): %s", resp.StatusCode, string(body))
	}

	var stats engine.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return fmt.Errorf("failed to decode stats JSON: %w", err)
	}

	if *asJSON {
		data, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	activeEst := stats.AccountedBytes / 360
	heapMB := float64(stats.HeapBytes) / (1024 * 1024)
	accountedMB := float64(stats.AccountedBytes) / (1024 * 1024)

	fmt.Println("==========================================================")
	fmt.Println("  GENESIS ENGINE STATUS & METRICS")
	fmt.Println("==========================================================")
	fmt.Printf("  Uptime:            %d seconds\n", stats.UptimeSeconds)
	fmt.Printf("  Total Received:    %d lines\n", stats.ReceivedCount)
	fmt.Printf("  Total Indexed:     %d lines\n", stats.IndexedCount)
	fmt.Printf("  Priority Lane:     %d lines\n", stats.PriorityCount)
	fmt.Printf("  Total Evicted:     %d lines\n", stats.Evicted)
	fmt.Printf("  Active Records:    ~%d lines (estimated)\n", activeEst)
	fmt.Printf("  Partitions:        %d active\n", stats.Partitions)
	fmt.Println("----------------------------------------------------------")
	fmt.Printf("  Accounted Index:   %.2f MB (%d bytes)\n", accountedMB, stats.AccountedBytes)
	fmt.Printf("  Go Heap Alloc:     %.2f MB (%d bytes)\n", heapMB, stats.HeapBytes)
	fmt.Println("----------------------------------------------------------")
	fmt.Printf("  Queue Depths:      normal=%d, priority=%d\n", stats.QueueDepths["normal"], stats.QueueDepths["priority"])
	fmt.Printf("  Dropped Logs:      queue_full=%d, malformed=%d, stopped=%d\n",
		stats.Dropped["queue_full"], stats.Dropped["malformed"], stats.Dropped["stopped"])
	fmt.Println("==========================================================")

	return nil
}
