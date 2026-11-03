package parser

import (
	"testing"
	"time"
)

func TestParseAutoDetect_PipeFormat(t *testing.T) {
	raw := []byte("1730000000|auth-api|ERROR|trace-9912|Database connection timeout\n")
	entry, err := ParseAny(1, raw, 65536, "default-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Timestamp != 1730000000 || entry.Service != "auth-api" || entry.Level != "ERROR" || entry.TraceID != "trace-9912" {
		t.Errorf("pipe format parsed incorrectly: %+v", entry)
	}
}

func TestParseAutoDetect_SyslogRFC3164(t *testing.T) {
	// <34>Oct 11 22:14:15 mymachine su: 'su root' failed for lonvick on /dev/pts/8
	raw := []byte("<34>Oct 11 22:14:15 web-host auth-svc: User login failed for root\n")
	entry, err := ParseAny(2, raw, 65536, "syslog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Service != "auth-svc" {
		t.Errorf("expected service 'auth-svc', got %q", entry.Service)
	}
	if entry.Level != "FATAL" {
		t.Errorf("expected level FATAL for PRI 34 (facility 4, severity 2), got %q", entry.Level)
	}
	if entry.Message != "User login failed for root" {
		t.Errorf("unexpected message: %q", entry.Message)
	}
}

func TestParseAutoDetect_SyslogRFC5424(t *testing.T) {
	// <165>1 2026-11-02T22:14:15.003Z mymachine.example.com order-api 1234 ID47 - Order failed out of stock
	raw := []byte("<165>1 2026-11-02T22:14:15Z myhost.local payment-api 9988 tr-7766 - Payment gateway timeout\n")
	entry, err := ParseAny(3, raw, 65536, "syslog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Service != "payment-api" {
		t.Errorf("expected service 'payment-api', got %q", entry.Service)
	}
	if entry.TraceID != "tr-7766" {
		t.Errorf("expected traceID 'tr-7766', got %q", entry.TraceID)
	}
	if entry.Message != "Payment gateway timeout" {
		t.Errorf("unexpected message: %q", entry.Message)
	}
	if entry.Timestamp <= 0 {
		t.Errorf("expected valid timestamp, got %d", entry.Timestamp)
	}
}

func TestParseAutoDetect_JSONLines(t *testing.T) {
	raw := []byte(`{"time":"2026-11-02T20:00:00Z","app":"checkout-svc","severity":"warn","trace_id":"trace-abc-123","msg":"Cart item price changed","extra_id":42}` + "\n")
	entry, err := ParseAny(4, raw, 65536, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Service != "checkout-svc" {
		t.Errorf("expected service 'checkout-svc', got %q", entry.Service)
	}
	if entry.Level != "WARN" {
		t.Errorf("expected level 'WARN', got %q", entry.Level)
	}
	if entry.TraceID != "trace-abc-123" {
		t.Errorf("expected trace 'trace-abc-123', got %q", entry.TraceID)
	}
	if entry.Message != "Cart item price changed" {
		t.Errorf("unexpected message: %q", entry.Message)
	}
	if entry.Attributes["extra_id"] != "42" {
		t.Errorf("expected attribute extra_id=42, got %v", entry.Attributes)
	}
}

func TestParseAutoDetect_JSONLines_LargeNumbers(t *testing.T) {
	raw := []byte(`{"time":1730592000000,"service":"billing","level":"info","msg":"charged","txn_id":987654321012345678,"amount":99.95}` + "\n")
	entry, err := ParseAny(10, raw, 65536, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Timestamp != 1730592000 {
		t.Errorf("expected unix timestamp 1730592000, got %d", entry.Timestamp)
	}
	if entry.Attributes["txn_id"] != "987654321012345678" {
		t.Errorf("expected exact integer string for txn_id, got %q", entry.Attributes["txn_id"])
	}
	if entry.Attributes["amount"] != "99.95" {
		t.Errorf("expected amount '99.95', got %q", entry.Attributes["amount"])
	}
}

func TestParseAutoDetect_PlainText_KeywordInference(t *testing.T) {
	now := time.Now().Unix()
	raw := []byte("2026-11-02 21:00:00 [ERROR] Failed to connect to Redis cluster at 10.0.0.1:6379\n")
	entry, err := ParseAny(5, raw, 65536, "redis-worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Service != "redis-worker" {
		t.Errorf("expected service 'redis-worker', got %q", entry.Service)
	}
	if entry.Level != "ERROR" {
		t.Errorf("expected inferred level 'ERROR', got %q", entry.Level)
	}
	if entry.Timestamp < now-5 || entry.Timestamp > now+5 {
		t.Errorf("expected timestamp near now (%d), got %d", now, entry.Timestamp)
	}
}
