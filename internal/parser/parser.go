package parser

import (
	"bytes"
	"errors"
	"sync"
)

var (
	ErrMalformedLog = errors.New("malformed log line: expected [Timestamp]|[Service]|[Level]|[TraceID]|[Message]")
	ErrEmptyInput   = errors.New("empty log line")
)

// Interned levels for zero-allocation reuse.
var (
	levelDebug = "DEBUG"
	levelInfo  = "INFO"
	levelWarn  = "WARN"
	levelError = "ERROR"
	levelFatal = "FATAL"
)

// Fast string pool for common service names.
var servicePool = sync.Map{}

func internService(b []byte) string {
	// Look up without allocation if possible
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

// ParseLogLine parses a raw byte slice into a structured LogEntry.
// Expected format: <timestamp>|<service>|<level>|<trace_id>|<message>
// Performs zero-copy slicing for string fields and interning where possible.
func ParseLogLine(id uint64, raw []byte) (LogEntry, error) {
	// Trim trailing newline or carriage return
	raw = bytes.TrimRight(raw, "\r\n")
	if len(raw) == 0 {
		return LogEntry{}, ErrEmptyInput
	}

	sizeBytes := len(raw)

	// Field 1: Timestamp
	idx1 := bytes.IndexByte(raw, '|')
	if idx1 == -1 {
		return LogEntry{}, ErrMalformedLog
	}
	tsBytes := raw[:idx1]
	ts, err := FastParseTimestamp(tsBytes)
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

	return LogEntry{
		ID:        id,
		Timestamp: ts,
		Service:   service,
		Level:     level,
		TraceID:   traceID,
		Message:   message,
		SizeBytes: sizeBytes + 64, // estimated in-memory footprint with struct overhead
	}, nil
}
