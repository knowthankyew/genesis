package cli

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

func inferLineLevel(line string, fallback string) string {
	upper := strings.ToUpper(line)
	switch {
	case strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC") || strings.Contains(upper, "CRIT"):
		return "FATAL"
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "EXCEPTION"):
		return "ERROR"
	case strings.Contains(upper, "WARN"):
		return "WARN"
	case strings.Contains(upper, "DEBUG") || strings.Contains(upper, "TRACE"):
		return "DEBUG"
	default:
		return fallback
	}
}

// RunPipe reads from standard input and streams lines into Genesis ingestion
func RunPipe(args []string) error {
	fs := flag.NewFlagSet("pipe", flag.ExitOnError)
	service := fs.String("service", "stdin", "Service name tag for piped logs")
	endpoint := fs.String("endpoint", "127.0.0.1:9000", "Genesis TCP ingestion address")
	_ = fs.Parse(args)

	conn, err := net.DialTimeout("tcp", *endpoint, 3*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to Genesis ingestion at %s: %w", *endpoint, err)
	}
	defer conn.Close()

	scanner := bufio.NewScanner(os.Stdin)
	var count uint64

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// If line already looks formatted (JSON, Syslog, or 5-field pipe), send as is
		// Otherwise wrap with standard pipe format and inferred level
		var toSend []byte
		if line[0] == '{' || line[0] == '<' || (line[0] >= '0' && line[0] <= '9' && bytes.Count(line, []byte("|")) >= 4) {
			toSend = append(line, '\n')
		} else {
			lvl := inferLineLevel(string(line), "INFO")
			toSend = []byte(fmt.Sprintf("%d|%s|%s||%s\n", time.Now().Unix(), *service, lvl, string(line)))
		}

		if _, err := conn.Write(toSend); err != nil {
			return fmt.Errorf("write error to Genesis socket: %w", err)
		}
		count++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin reading error: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[genesis pipe] Successfully streamed %d lines to %s\n", count, *endpoint)
	return nil
}
