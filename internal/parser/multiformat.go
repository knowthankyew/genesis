package parser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseAny auto-detects the format of raw and returns a structured LogEntry.
// Formats supported:
// 1. Pipe-delimited: <timestamp>|<service>|<level>|<trace_id>|<message>
// 2. Syslog RFC 5424 / RFC 3164
// 3. JSON lines with standard key mapping
// 4. Plain text with keyword level inference
func ParseAny(id uint64, raw []byte, maxLineLen int, defaultService string) (LogEntry, error) {
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

	if defaultService == "" {
		defaultService = "default"
	}

	// 1. Check for JSON lines: starts with '{'
	if raw[0] == '{' && raw[len(raw)-1] == '}' {
		return parseJSONLines(id, raw, defaultService)
	}

	// 2. Check for Syslog: starts with '<'
	if raw[0] == '<' {
		idxClose := bytes.IndexByte(raw, '>')
		if idxClose > 1 && idxClose <= 5 {
			return parseSyslog(id, raw, idxClose, defaultService)
		}
	}

	// 3. Check for Pipe format: starts with digits followed by '|'
	idx1 := bytes.IndexByte(raw, '|')
	if idx1 > 0 && isAllDigits(raw[:idx1]) {
		return ParseLogLine(id, raw, maxLineLen)
	}

	// 4. Plain text format
	return parsePlainText(id, raw, defaultService)
}

func isAllDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func parseJSONLines(id uint64, raw []byte, defaultService string) (LogEntry, error) {
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return parsePlainText(id, raw, defaultService)
	}

	// Extract Timestamp
	var ts int64
	for _, k := range []string{"time", "ts", "timestamp", "@timestamp"} {
		if val, ok := obj[k]; ok {
			ts = parseTimestampValue(val)
			delete(obj, k)
			break
		}
	}
	if ts <= 0 {
		ts = time.Now().Unix()
	}

	// Extract Level
	var level string
	for _, k := range []string{"level", "severity", "lvl"} {
		if val, ok := obj[k]; ok {
			level = normalizeLevel(stringifyJSONValue(val))
			delete(obj, k)
			break
		}
	}
	if level == "" {
		level = levelInfo
	}

	// Extract Service
	var service string
	for _, k := range []string{"service", "app", "logger", "name"} {
		if val, ok := obj[k]; ok {
			service = internService([]byte(stringifyJSONValue(val)))
			delete(obj, k)
			break
		}
	}
	if service == "" {
		service = defaultService
	}

	// Extract TraceID
	var traceID string
	for _, k := range []string{"trace_id", "traceId", "trace"} {
		if val, ok := obj[k]; ok {
			traceID = stringifyJSONValue(val)
			delete(obj, k)
			break
		}
	}

	// Extract Message
	var message string
	for _, k := range []string{"msg", "message"} {
		if val, ok := obj[k]; ok {
			message = stringifyJSONValue(val)
			delete(obj, k)
			break
		}
	}

	// Remaining keys as attributes
	var attrs map[string]string
	if len(obj) > 0 {
		attrs = make(map[string]string, len(obj))
		for k, v := range obj {
			attrs[k] = stringifyJSONValue(v)
		}
	}

	sizeBytes := 360 + len(traceID) + len(message) + (len(attrs) * 48)

	return LogEntry{
		ID:         id,
		Timestamp:  ts,
		Service:    service,
		Level:      level,
		TraceID:    traceID,
		Message:    message,
		Attributes: attrs,
		SizeBytes:  sizeBytes,
	}, nil
}

func stringifyJSONValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		return fmt.Sprint(t)
	}
}

func parseTimestampValue(val any) int64 {
	switch v := val.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			if n > 1e12 {
				return n / 1000
			}
			return n
		}
		if f, err := v.Float64(); err == nil {
			n := int64(f)
			if n > 1e12 {
				return n / 1000
			}
			return n
		}
	case float64:
		n := int64(v)
		if n > 1e12 { // Milliseconds
			return n / 1000
		}
		return n
	case int64:
		if v > 1e12 {
			return v / 1000
		}
		return v
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.Unix()
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.Unix()
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n > 1e12 {
				return n / 1000
			}
			return n
		}
	}
	return 0
}

func normalizeLevel(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "DBG", "DEBUG", "TRACE":
		return levelDebug
	case "INF", "INFO", "NOTICE":
		return levelInfo
	case "WRN", "WARN", "WARNING":
		return levelWarn
	case "ERR", "ERROR":
		return levelError
	case "CRIT", "CRITICAL", "FATAL", "EMERG", "EMERGENCY", "PANIC":
		return levelFatal
	default:
		return s
	}
}

// Syslog severity mapping (PRI = facility * 8 + severity)
var syslogSeverityMap = []string{
	levelFatal, // 0: Emergency
	levelFatal, // 1: Alert
	levelFatal, // 2: Critical
	levelError, // 3: Error
	levelWarn,  // 4: Warning
	levelInfo,  // 5: Notice
	levelInfo,  // 6: Informational
	levelDebug, // 7: Debug
}

func parseSyslog(id uint64, raw []byte, idxClose int, defaultService string) (LogEntry, error) {
	priBytes := raw[1:idxClose]
	pri, err := strconv.Atoi(string(priBytes))
	if err != nil {
		return parsePlainText(id, raw, defaultService)
	}

	severity := pri & 7
	level := syslogSeverityMap[severity]

	rest := bytes.TrimSpace(raw[idxClose+1:])

	// Check if RFC 5424 (starts with "1 ")
	if bytes.HasPrefix(rest, []byte("1 ")) {
		return parseSyslogRFC5424(id, rest[2:], level, defaultService)
	}

	// RFC 3164 (legacy BSD syslog)
	return parseSyslogRFC3164(id, rest, level, defaultService)
}

func parseSyslogRFC5424(id uint64, rest []byte, level, defaultService string) (LogEntry, error) {
	// Format: TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
	parts := strings.SplitN(string(rest), " ", 6)
	if len(parts) < 6 {
		return parsePlainText(id, rest, defaultService)
	}

	tsStr, _, appName, _, msgID, msg := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]

	var ts int64
	if t, err := time.Parse(time.RFC3339Nano, tsStr); err == nil {
		ts = t.Unix()
	} else if t, err := time.Parse(time.RFC3339, tsStr); err == nil {
		ts = t.Unix()
	} else {
		ts = time.Now().Unix()
	}

	service := appName
	if service == "-" || service == "" {
		service = defaultService
	}

	traceID := msgID
	if traceID == "-" {
		traceID = ""
	}

	// Skip structured data if present: e.g. [exampleSDID...] msg or "- msg"
	if strings.HasPrefix(msg, "[") {
		idxClose := strings.Index(msg, "]")
		if idxClose != -1 && idxClose+1 < len(msg) {
			msg = strings.TrimSpace(msg[idxClose+1:])
		}
	} else if strings.HasPrefix(msg, "- ") {
		msg = strings.TrimSpace(msg[2:])
	}

	sizeBytes := 360 + len(traceID) + len(msg)

	return LogEntry{
		ID:        id,
		Timestamp: ts,
		Service:   internService([]byte(service)),
		Level:     level,
		TraceID:   traceID,
		Message:   msg,
		SizeBytes: sizeBytes,
	}, nil
}

func parseSyslogRFC3164(id uint64, rest []byte, level, defaultService string) (LogEntry, error) {
	// Format: MMM DD HH:MM:SS HOSTNAME TAG: MSG
	s := string(rest)
	if len(s) < 16 {
		return parsePlainText(id, rest, defaultService)
	}

	// Date prefix: "Oct 11 22:14:15"
	timePart := s[:15]
	var ts int64
	now := time.Now()
	if t, err := time.ParseInLocation("Jan _2 15:04:05", timePart, now.Location()); err == nil {
		// RFC 3164 doesn't include year, use current year
		ts = time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location()).Unix()
	} else {
		ts = now.Unix()
	}

	remainder := strings.TrimSpace(s[15:])
	// Remainder format: hostname tag: msg OR tag: msg
	idxColon := strings.Index(remainder, ":")
	var service, msg string
	if idxColon != -1 {
		prefix := strings.TrimSpace(remainder[:idxColon])
		msg = strings.TrimSpace(remainder[idxColon+1:])

		// Check if prefix has hostname + tag
		parts := strings.Fields(prefix)
		if len(parts) >= 2 {
			service = parts[len(parts)-1]
		} else if len(parts) == 1 {
			service = parts[0]
		}
	} else {
		service = defaultService
		msg = remainder
	}

	if service == "" {
		service = defaultService
	}

	sizeBytes := 360 + len(msg)

	return LogEntry{
		ID:        id,
		Timestamp: ts,
		Service:   internService([]byte(service)),
		Level:     level,
		Message:   msg,
		SizeBytes: sizeBytes,
	}, nil
}

func parsePlainText(id uint64, raw []byte, defaultService string) (LogEntry, error) {
	s := string(raw)
	now := time.Now().Unix()

	// Infer log level by keywords
	upper := strings.ToUpper(s)
	level := levelInfo
	switch {
	case strings.Contains(upper, "FATAL") || strings.Contains(upper, "PANIC") || strings.Contains(upper, "CRIT"):
		level = levelFatal
	case strings.Contains(upper, "ERROR") || strings.Contains(upper, "ERR") || strings.Contains(upper, "EXCEPTION"):
		level = levelError
	case strings.Contains(upper, "WARN") || strings.Contains(upper, "WARNING"):
		level = levelWarn
	case strings.Contains(upper, "DEBUG") || strings.Contains(upper, "TRACE"):
		level = levelDebug
	}

	sizeBytes := 360 + len(s)

	return LogEntry{
		ID:        id,
		Timestamp: now,
		Service:   internService([]byte(defaultService)),
		Level:     level,
		Message:   s,
		SizeBytes: sizeBytes,
	}, nil
}
