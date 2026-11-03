package main

import (
	"bufio"
	"context"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/knowthankyew/genesis/internal/engine"
)

var (
	tcpAddr            = flag.String("tcp", "127.0.0.1:9000", "TCP ingestion address")
	udpAddr            = flag.String("udp", "127.0.0.1:9001", "UDP ingestion address")
	httpAddr           = flag.String("http", "127.0.0.1:8080", "HTTP query API address")
	allowAllInterfaces = flag.Bool("allow-all-interfaces", false, "Explicitly allow binding on all interfaces (0.0.0.0 / :port)")
	maxMemMB           = flag.Int64("max-mem-mb", 512, "Maximum in-memory index size in MB")
	maxConns           = flag.Int64("max-conns", 20000, "Maximum simultaneous client TCP connections")
	maxLineLen         = flag.Int("max-line-len", 65536, "Maximum log line length in bytes")
)

var (
	activeTCPConnections atomic.Int64
	udpBufPool = sync.Pool{
		New: func() any {
			b := make([]byte, 65536) // 64 KB UDP buffer (Fix #5)
			return &b
		},
	}
)

func main() {
	flag.Parse()

	log.Println("==========================================================")
	log.Println("  GENESIS: High-Performance In-Memory Log Engine")
	log.Println("==========================================================")

	// Insecure bind check (Fix #8)
	if isBindingAllInterfaces(*httpAddr) && !*allowAllInterfaces {
		log.Println("WARNING: Binding HTTP on all interfaces (:port or 0.0.0.0) without -allow-all-interfaces flag!")
		log.Println("Defaulting HTTP bind to 127.0.0.1:8080 for security. Use -allow-all-interfaces to override.")
		*httpAddr = "127.0.0.1:8080"
	}

	eng := engine.NewEngine(engine.EngineConfig{
		MaxMemoryBytes:   *maxMemMB * 1024 * 1024,
		QueueCapacity:    250000,
		PriorityQueueCap: 50000,
		MaxLineLength:    *maxLineLen,
	})
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start HTTP Query Server with timeouts (Fix #7)
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/query", eng.HandleQuery)
	httpMux.HandleFunc("/stats", eng.HandleStats)

	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("[HTTP] Query Server listening on %s\n", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[HTTP] Error: %v", err)
		}
	}()

	// 2. Start TCP Listener
	go startTCPServer(ctx, *tcpAddr, eng, *maxConns)

	// 3. Start UDP Listener (64 KB buffer)
	go startUDPServer(ctx, *udpAddr, eng)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("\n[Shutdown] Shutting down Genesis gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Println("[Shutdown] Complete.")
}

func isBindingAllInterfaces(addr string) bool {
	return strings.HasPrefix(addr, ":") || strings.HasPrefix(addr, "0.0.0.0:") || addr == "0.0.0.0"
}

func startTCPServer(ctx context.Context, addr string, eng *engine.Engine, maxConnections int64) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[TCP] Listen error: %v", err)
	}
	defer listener.Close()

	log.Printf("[TCP] Ingestion Server listening on %s\n", addr)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}

		if activeTCPConnections.Load() >= maxConnections {
			_ = conn.Close()
			continue
		}

		activeTCPConnections.Add(1)
		go func(c net.Conn) {
			defer func() {
				_ = c.Close()
				activeTCPConnections.Add(-1)
			}()
			handleTCPConnection(c, eng)
		}(conn)
	}
}

func handleTCPConnection(conn net.Conn, eng *engine.Engine) {
	// 4KB reader per connection to maintain small footprint under 10k+ connections
	reader := bufio.NewReaderSize(conn, 4096)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_ = eng.Ingest(line)
		}
		if err != nil {
			if err != io.EOF {
				// connection closed or reset
			}
			break
		}
	}
}

func startUDPServer(ctx context.Context, addr string, eng *engine.Engine) {
	lAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatalf("[UDP] Resolve error: %v", err)
	}

	conn, err := net.ListenUDP("udp", lAddr)
	if err != nil {
		log.Fatalf("[UDP] Listen error: %v", err)
	}
	defer conn.Close()

	log.Printf("[UDP] Ingestion Server listening on %s (64KB buffer)\n", addr)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	bufPtr := udpBufPool.Get().(*[]byte)
	defer udpBufPool.Put(bufPtr)
	buf := *bufPtr

	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		if n > 0 {
			_ = eng.Ingest(buf[:n])
		}
	}
}
