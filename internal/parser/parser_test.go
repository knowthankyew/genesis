package parser

import (
	"testing"
)

func TestParseLogLine(t *testing.T) {
	line := []byte("1730000000|auth-api|ERROR|trace-998877|Database connection timeout on replica-2\n")
	entry, err := ParseLogLine(1, line)
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

func BenchmarkParseLogLine(b *testing.B) {
	line := []byte("1730000000|auth-api|ERROR|trace-998877|Database connection timeout on replica-2\n")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := ParseLogLine(uint64(i), line)
		if err != nil {
			b.Fatal(err)
		}
	}
}
