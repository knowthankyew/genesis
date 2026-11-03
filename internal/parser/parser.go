package parser

import (
	"bytes"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	DefaultMaxLineLength   = 65536 // 64 KB
	MaxServicePoolSize     = 1024
	EntryBaseOverheadBytes = 360
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

var (
	servicePool     sync.Map
	servicePoolSize atomic.Int64
)
var truncatedLines atomic.Uint64

func TruncatedLinesCount() uint64 {
	return truncatedLines.Load()
}

// RecordTruncatedLine increments the truncated line counter when an upstream reader
// (such as a bounded TCP ReadSlice buffer) truncates an oversized incoming line.
func RecordTruncatedLine() {
	truncatedLines.Add(1)
}

func internService(b []byte) string {
	if s, ok := servicePool.Load(string(b)); ok {
		return s.(string)
	}
	s := string(b)
	if servicePoolSize.Load() < MaxServicePoolSize {
		if _, loaded := servicePool.LoadOrStore(s, s); !loaded {
			servicePoolSize.Add(1)
		}
	}
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

// ExtractLevel parses the log level to accurately route critical logs to the priority lane.
// Fast paths for Syslog, JSON, Pipe format, and Plain text.
func ExtractLevel(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}

	// 1. Syslog format: starts with '<'
	if raw[0] == '<' {
		idxClose := bytes.IndexByte(raw, '>')
		if idxClose > 1 && idxClose <= 5 {
			pri, err := strconv.Atoi(string(raw[1:idxClose]))
			if err == nil {
				severity := pri & 7
				if severity <= 2 {
					return levelFatal
				} else if severity == 3 {
					return levelError
				}
				return ""
			}
		}
	}

	// 2. JSON format: starts with '{'
	if raw[0] == '{' {
		lower := bytes.ToLower(raw)
		if bytes.Contains(lower, []byte(`"level":"fatal"`)) ||
			bytes.Contains(lower, []byte(`"severity":"fatal"`)) ||
			bytes.Contains(lower, []byte(`"level":"crit"`)) ||
			bytes.Contains(lower, []byte(`"severity":"crit"`)) ||
			bytes.Contains(lower, []byte(`"level":"panic"`)) {
			return levelFatal
		}
		if bytes.Contains(lower, []byte(`"level":"error"`)) ||
			bytes.Contains(lower, []byte(`"severity":"error"`)) ||
			bytes.Contains(lower, []byte(`"level":"err"`)) {
			return levelError
		}
		return ""
	}

	// 3. Pipe format: <timestamp>|<service>|<level>|...
	idx1 := bytes.IndexByte(raw, '|')
	if idx1 != -1 {
		rest := raw[idx1+1:]
		idx2 := bytes.IndexByte(rest, '|')
		if idx2 != -1 {
			rest = rest[idx2+1:]
			idx3 := bytes.IndexByte(rest, '|')
			if idx3 == -1 {
				return internLevel(bytes.TrimRight(rest, "\r\n"))
			}
			return internLevel(rest[:idx3])
		}
	}

	// 4. Plain text fallback: check for FATAL/ERROR keywords
	upper := bytes.ToUpper(raw)
	if bytes.Contains(upper, []byte("FATAL")) || bytes.Contains(upper, []byte("PANIC")) || bytes.Contains(upper, []byte("CRIT")) {
		return levelFatal
	}
	if bytes.Contains(upper, []byte("ERROR")) {
		return levelError
	}

	return ""
}

// LogEntry represents a parsed, structured log object.
type LogEntry struct {
	ID         uint64            `json:"id"`
	Timestamp  int64             `json:"timestamp"`
	Service    string            `json:"service"`
	Level      string            `json:"level"`
	TraceID    string            `json:"trace_id"`
	Message    string            `json:"message"`
	Attributes map[string]string `json:"attributes,omitempty"`
	SizeBytes  int               `json:"size_bytes"`
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
	// - Map node & bucket overhead: ~80 bytes
	// - SkipList SkipNode + forward slice + prev pointer: ~96 bytes
	// - Inverted indices map nodes: ~64 bytes
	// - String contents & headers: len(traceID) + len(message) + ~32 bytes
	// Calibrated overhead: ~360 bytes + string lengths
	sizeBytes := EntryBaseOverheadBytes + len(traceID) + len(message)

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
