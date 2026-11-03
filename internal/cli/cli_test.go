package cli

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/knowthankyew/genesis/internal/engine"
	"github.com/knowthankyew/genesis/internal/parser"
)

func TestCLI_IsBindingAllInterfaces(t *testing.T) {
	tests := []struct {
		addr     string
		expected bool
	}{
		{":8080", true},
		{"0.0.0.0:8080", true},
		{"0.0.0.0", true},
		{"127.0.0.1:8080", false},
		{"localhost:8080", false},
		{"192.168.1.100:8080", false},
	}

	for _, tc := range tests {
		got := IsBindingAllInterfaces(tc.addr)
		if got != tc.expected {
			t.Errorf("IsBindingAllInterfaces(%q) = %v; expected %v", tc.addr, got, tc.expected)
		}
	}
}

func TestCLI_HandleTCPConnection_BoundedAccumulator(t *testing.T) {
	eng := engine.NewEngine(engine.EngineConfig{MaxLineLength: 8192})
	defer eng.Close()

	beforeTrunc := parser.TruncatedLinesCount()
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleTCPConnection(serverConn, eng, 8192)
	}()

	// 1. Multi-chunk line <= maxLineLen (6000 bytes > 4096 reader buffer, <= 8192 maxLineLen)
	mediumMsg := strings.Repeat("a", 5900)
	_, _ = clientConn.Write([]byte("1730000001|tcp-svc|INFO|tr-1|" + mediumMsg + "\n"))

	// 2. Oversized line > maxLineLen (12000 bytes > 8192 maxLineLen)
	oversizedMsg := strings.Repeat("b", 12000)
	_, _ = clientConn.Write([]byte("1730000002|tcp-svc|ERROR|tr-2|" + oversizedMsg + "\n"))
	_ = clientConn.Close()
	<-done

	deadline := time.Now().Add(2 * time.Second)
	for eng.IndexedCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if eng.IndexedCount() != 2 {
		t.Fatalf("expected 2 indexed lines from TCP pipe, got %d", eng.IndexedCount())
	}
	if parser.TruncatedLinesCount() <= beforeTrunc {
		t.Fatalf("expected truncated line counter to increment on oversized TCP line (before=%d, after=%d)", beforeTrunc, parser.TruncatedLinesCount())
	}
}
