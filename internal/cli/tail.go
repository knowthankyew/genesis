package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/knowthankyew/genesis/internal/parser"
)

func RunTail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	httpURL := fs.String("http", "http://127.0.0.1:8080", "Genesis HTTP address")
	level := fs.String("level", "", "Filter by log level")
	service := fs.String("service", "", "Filter by service name")
	grep := fs.String("grep", "", "Search substring in message")
	token := fs.String("token", "", "Bearer auth token (or GENESIS_AUTH_TOKEN)")
	_ = fs.Bool("f", true, "Follow stream (standard flag)")
	_ = fs.Parse(args)

	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("GENESIS_AUTH_TOKEN")
	}

	params := url.Values{}
	if *level != "" {
		params.Set("level", strings.ToUpper(*level))
	}
	if *service != "" {
		params.Set("service", *service)
	}
	if *grep != "" {
		params.Set("grep", *grep)
	}

	baseURL := strings.TrimRight(*httpURL, "/")
	reqURL := fmt.Sprintf("%s/tail?%s", baseURL, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create tail request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to Genesis live tail at %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tail connection rejected with status: %d", resp.StatusCode)
	}

	fmt.Printf("[genesis tail] Live streaming from %s (Ctrl+C to stop)...\n", baseURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n[genesis tail] Stream stopped.")
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var entry parser.LogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue
		}

		t := time.Unix(entry.Timestamp, 0).UTC().Format("15:04:05")
		trace := entry.TraceID
		if trace == "" {
			trace = "-"
		}
		fmt.Printf("%s [%-5s] %-14s [%s] %s\n", t, entry.Level, entry.Service, trace, entry.Message)
	}

	return scanner.Err()
}
