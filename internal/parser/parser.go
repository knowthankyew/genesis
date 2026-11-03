package parser

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
)

const (
	DefaultMaxLineLength = 65536 // 64 KB
)

var (
	ErrMalformedLog = errors.New("malformed log line: expected [Timestamp]|[Service]|[Level]|[TraceID]|[Message]")
	ErrEmptyInput   = errors.New("empty log line")
)

var (
	levelDebug = "DEBUG"
	levelInfo  = "INFO"
	levelWarn  = "WARN"
	levelError = "ERROR"
	levelFatal = "FATAL"
)

var servicePool = sync.Map{}
var truncatedLines atomic.Uint64

func TruncatedLinesCount() uint64 {
	return truncatedLines.Load()
}

func internService(b []byte) string {
	if s, ok := servicePool.Load(string(b)); ok {
		return s.(string)
	}
	s := string(b)
	servicePool.Store(s, s)
	return s
}

func internLevel(b []byte) string {
	switch {
	case bytes.Equal(b, []byte("DEBUG")):
		return levelDebug
	case bytes.Equal(b, []byte("INFO")):
		return levelInfo
	case bytes.Equal(b, []byte("WARN")):
		return levelWarn
	case bytes.Equal(b, []byte("ERROR")):
		return levelError
	case bytes.Equal(b, []byte("FATAL")):
		return levelFatal
	default:
		return string(b)
	}
}

// ExtractLevel parses only field 3 (LogLevel) to accurately route to priority lane.
// Format: <timestamp>|<service>|<level>|...
func ExtractLevel(raw []byte) string {
	idx1 := bytes.IndexByte(raw, '|')
	if idx1 == -1 {
		return ""
	}
	rest := raw[idx1+1:]

	idx2 := bytes.IndexByte(rest, '|')
	if idx2 == -1 {
		return ""
	}
	rest = rest[idx2+1:]

	idx3 := bytes.IndexByte(rest, '|')
	if idx3 == -1 {
		return internLevel(bytes.TrimRight(rest, "\r\n"))
	}
	return internLevel(rest[:idx3])
}

// LogEntry represents a parsed, structured log object.
type LogEntry struct {
	ID        uint64 `json:"id"`
	Timestamp int64  `json:"timestamp"`
	Service   string `json:"service"`
	Level     string `json:"level"`
	TraceID   string `json:"trace_id"`
	Message   string `json:"message"`
	SizeBytes int    `json:"size_bytes"`
}

// FastParseTimestamp parses unix timestamp from ASCII bytes without allocations.
func FastParseTimestamp(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, errors.New("empty timestamp")
	}
	var n int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid character in timestamp")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// ParseLogLine parses a raw byte slice into a structured LogEntry with maxLineLen truncation.
func ParseLogLine(id uint64, raw []byte, maxLineLen int) (LogEntry, error) {
	if maxLineLen <= 0 {
		maxLineLen = DefaultMaxLineLength
	}

	raw = bytes.TrimRight(raw, "\r\n")
	if len(raw) == 0 {
		return LogEntry{}, ErrEmptyInput
	}

	if len(raw) > maxLineLen {
		raw = raw[:maxLineLen]
		truncatedLines.Add(1)
	}

	// Field 1: Timestamp
	idx1 := bytes.IndexByte(raw, '|')
	if idx1 == -1 {
		return LogEntry{}, ErrMalformedLog
	}
	ts, err := FastParseTimestamp(raw[:idx1])
	if err != nil {
		return LogEntry{}, err
	}
	rest := raw[idx1+1:]

	// Field 2: Service
	idx2 := bytes.IndexByte(rest, '|')
	if idx2 == -1 {
		return LogEntry{}, ErrMalformedLog
	}
	service := internService(rest[:idx2])
	rest = rest[idx2+1:]

	// Field 3: Level
	idx3 := bytes.IndexByte(rest, '|')
	if idx3 == -1 {
		return LogEntry{}, ErrMalformedLog
	}
	level := internLevel(rest[:idx3])
	rest = rest[idx3+1:]

	// Field 4: TraceID
	idx4 := bytes.IndexByte(rest, '|')
	if idx4 == -1 {
		return LogEntry{}, ErrMalformedLog
	}
	traceID := string(rest[:idx4])
	message := string(rest[idx4+1:])

	// Calibrated memory overhead per entry in 64-bit runtime:
	// - LogEntry struct on heap / map slot: ~88 bytes
	// - Map node & bucket overhead: ~48 bytes
	// - SkipList SkipNode + forward slice: ~64 bytes
	// - Inverted indices map nodes: ~32 bytes
	// - String contents: len(traceID) + len(message)
	// Base calibrated overhead: ~240 bytes + string lengths
	sizeBytes := 240 + len(traceID) + len(message)

	return LogEntry{
		ID:        id,
		Timestamp: ts,
		Service:   service,
		Level:     level,
		TraceID:   traceID,
		Message:   message,
		SizeBytes: sizeBytes,
	}, nil
}
