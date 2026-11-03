package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/knowthankyew/genesis/internal/parser"
)

func RunQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	httpURL := fs.String("http", "http://127.0.0.1:8080", "Genesis HTTP address")
	since := fs.String("since", "1h", "Time window to query (e.g. 15m, 1h, 24h, 30s)")
	level := fs.String("level", "", "Filter by log level (DEBUG, INFO, WARN, ERROR, FATAL)")
	service := fs.String("service", "", "Filter by service name")
	trace := fs.String("trace", "", "Filter by exact trace ID")
	grep := fs.String("grep", "", "Search substring in message")
	limit := fs.Int("limit", 100, "Maximum number of logs to return")
	asJSON := fs.Bool("json", false, "Output raw JSON")
	token := fs.String("token", "", "Bearer authentication token (or GENESIS_AUTH_TOKEN)")
	_ = fs.Parse(args)

	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("GENESIS_AUTH_TOKEN")
	}

	dur, err := time.ParseDuration(*since)
	if err != nil {
		return fmt.Errorf("invalid --since duration %q (use e.g. 15m, 1h, 24h): %w", *since, err)
	}

	start := time.Now().Add(-dur).Unix()
	end := time.Now().Unix() + 10

	params := url.Values{}
	params.Set("start", strconv.FormatInt(start, 10))
	params.Set("end", strconv.FormatInt(end, 10))
	if *level != "" {
		params.Set("level", strings.ToUpper(*level))
	}
	if *service != "" {
		params.Set("service", *service)
	}
	if *trace != "" {
		params.Set("trace", *trace)
	}
	if *grep != "" {
		params.Set("grep", *grep)
	}
	params.Set("limit", strconv.Itoa(*limit))
	params.Set("order", "asc")

	baseURL := strings.TrimRight(*httpURL, "/")
	reqURL := fmt.Sprintf("%s/query?%s", baseURL, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create query request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("query request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("query error (status %d): %s", resp.StatusCode, string(body))
	}

	var qResp struct {
		Count     int               `json:"count"`
		LatencyUs int64             `json:"latency_us"`
		Logs      []parser.LogEntry `json:"logs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&qResp); err != nil {
		return fmt.Errorf("failed to decode response JSON: %w", err)
	}

	if *asJSON {
		enc := json.NewEncoder(io.Discard)
		_ = enc
		data, _ := json.MarshalIndent(qResp.Logs, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Queried %d records in %d µs (window: %s)\n", qResp.Count, qResp.LatencyUs, *since)
	fmt.Println("--------------------------------------------------------------------------------")
	for _, entry := range qResp.Logs {
		t := time.Unix(entry.Timestamp, 0).UTC().Format("2006-01-02 15:04:05")
		trace := entry.TraceID
		if trace == "" {
			trace = "-"
		}
		fmt.Printf("%s  [%-5s]  %-14s  [%s]  %s\n", t, entry.Level, entry.Service, trace, entry.Message)
	}
	return nil
}
