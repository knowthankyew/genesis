package parser

import (
	"strings"
	"testing"
)

func TestParseLogLine(t *testing.T) {
	line := []byte("1730000000|auth-api|ERROR|trace-998877|Database connection timeout on replica-2\n")
	entry, err := ParseLogLine(1, line, 65536)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.ID != 1 {
		t.Errorf("expected ID 1, got %d", entry.ID)
	}
	if entry.Timestamp != 1730000000 {
		t.Errorf("expected timestamp 1730000000, got %d", entry.Timestamp)
	}
	if entry.Service != "auth-api" {
		t.Errorf("expected service auth-api, got %s", entry.Service)
	}
	if entry.Level != "ERROR" {
		t.Errorf("expected level ERROR, got %s", entry.Level)
	}
	if entry.TraceID != "trace-998877" {
		t.Errorf("expected trace-998877, got %s", entry.TraceID)
	}
	if entry.Message != "Database connection timeout on replica-2" {
		t.Errorf("unexpected message: %s", entry.Message)
	}
}

// Regression Test 3: ExtractLevel parses only field 3, not message substrings
func TestExtractLevel_NoSubstringFalsePositive(t *testing.T) {
	// A message containing "|ERROR|" at INFO level
	line := []byte("1730000000|auth-api|INFO|trace-1234|User requested search for keyword |ERROR| in document\n")
	level := ExtractLevel(line)
	if level != "INFO" {
		t.Fatalf("expected level INFO, got %s (failed substring false-positive test)", level)
	}

	fatalLine := []byte("1730000000|auth-api|FATAL|trace-1234|System crash\n")
	if lvl := ExtractLevel(fatalLine); lvl != "FATAL" {
		t.Fatalf("expected FATAL, got %s", lvl)
	}

	errorLine := []byte("1730000000|auth-api|ERROR|trace-1234|Fail\n")
	if lvl := ExtractLevel(errorLine); lvl != "ERROR" {
		t.Fatalf("expected ERROR, got %s", lvl)
	}
}

// Regression Test 5: Configurable max line length truncation
func TestParseLogLine_Truncation(t *testing.T) {
	initialTruncated := TruncatedLinesCount()

	// 100-byte message with max length 50
	longMsg := strings.Repeat("A", 100)
	raw := []byte("1730000000|svc|INFO|tr|" + longMsg + "\n")

	entry, err := ParseLogLine(1, raw, 40)
	if err != nil {
		t.Fatalf("unexpected error parsing truncated line: %v", err)
	}

	if len(entry.Message) > 40 {
		t.Errorf("message not truncated: len = %d", len(entry.Message))
	}

	if TruncatedLinesCount() <= initialTruncated {
		t.Errorf("expected truncatedLines counter to increment")
	}
}

// FuzzParseLogLine ensures parser never panics on arbitrary malformed bytes
func FuzzParseLogLine(f *testing.F) {
	seeds := [][]byte{
		[]byte("1730000000|auth-api|ERROR|trace-123|Database timeout\n"),
		[]byte("1730000001|payment|INFO|tr-99|Payment processed\n"),
		[]byte("||||\n"),
		[]byte("invalid\n"),
		[]byte(""),
		[]byte("1730000000|svc|INFO|tr||ERROR|\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseLogLine(1, data, 1024)
		_ = ExtractLevel(data)
	})
}
