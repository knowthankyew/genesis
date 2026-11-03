package cli

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
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/knowthankyew/genesis/internal/engine"
	"github.com/knowthankyew/genesis/internal/ui"
)

var (
	activeTCPConnections atomic.Int64
	udpBufPool           = sync.Pool{
		New: func() any {
			b := make([]byte, 65536) // 64 KB UDP buffer
			return &b
		},
	}
)

func RunServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	tcpAddr := fs.String("tcp", "127.0.0.1:9000", "TCP ingestion address")
	udpAddr := fs.String("udp", "127.0.0.1:9001", "UDP ingestion address")
	syslogAddr := fs.String("syslog", "127.0.0.1:5514", "UDP Syslog ingestion address")
	httpAddr := fs.String("http", "127.0.0.1:8080", "HTTP query & UI API address")
	allowAllInterfaces := fs.Bool("allow-all-interfaces", false, "Explicitly allow binding on all interfaces (0.0.0.0 / :port)")
	maxMemMB := fs.Int64("max-mem-mb", 350, "Maximum in-memory index size in MB (default 350MB to guarantee <=512MB total heap)")
	maxConns := fs.Int64("max-conns", 20000, "Maximum simultaneous client TCP connections")
	maxLineLen := fs.Int("max-line-len", 65536, "Maximum log line length in bytes")

	_ = fs.Parse(args)

	// Enforce 480MB memory ceiling on Go runtime GC so total heap never breaches 512MB
	debug.SetMemoryLimit(480 * 1024 * 1024)

	log.Println("==========================================================")
	log.Println("  GENESIS: High-Performance In-Memory Log Engine")
	log.Println("==========================================================")

	// Insecure bind check
	if IsBindingAllInterfaces(*httpAddr) && !*allowAllInterfaces {
		log.Println("WARNING: Binding HTTP on all interfaces (:port or 0.0.0.0) without -allow-all-interfaces flag!")
		log.Println("Defaulting HTTP bind to 127.0.0.1:8080 for security. Use -allow-all-interfaces to override.")
		*httpAddr = "127.0.0.1:8080"
	}

	eng := engine.NewEngine(engine.EngineConfig{
		MaxMemoryBytes:   *maxMemMB * 1024 * 1024,
		QueueCapacity:    250000,
		PriorityQueueCap: 50000,
		MaxLineLength:    *maxLineLen,
		BucketDuration:   5, // 5-second time partitions
	})
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Setup HTTP Server (UI, Query, Stats, Tail SSE, Burn)
	httpMux := http.NewServeMux()
	httpMux.Handle("/", ui.Handler())
	httpMux.HandleFunc("/query", eng.HandleQuery)
	httpMux.HandleFunc("/stats", eng.HandleStats)
	httpMux.HandleFunc("/tail", eng.HandleTail)
	httpMux.HandleFunc("/logs", eng.HandleBurn)
	httpMux.HandleFunc("/burn", eng.HandleBurn)

	httpServer := &http.Server{
		Addr:              *httpAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("[HTTP] Web UI & API listening on http://%s\n", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[HTTP] Error: %v", err)
		}
	}()

	// 2. Start TCP Listener
	go startTCPServer(ctx, *tcpAddr, eng, *maxConns)

	// 3. Start UDP Listener (Pipe/generic format)
	go startUDPServer(ctx, *udpAddr, eng, "[UDP]")

	// 4. Start Syslog UDP Listener
	if *syslogAddr != "" {
		go startUDPServer(ctx, *syslogAddr, eng, "[Syslog UDP]")
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("\n[Shutdown] Shutting down Genesis gracefully...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Println("[Shutdown] Complete.")
	return nil
}

func IsBindingAllInterfaces(addr string) bool {
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
	reader := bufio.NewReaderSize(conn, 4096)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_ = eng.Ingest(line)
		}
		if err != nil {
			if err != io.EOF {
				// connection closed
			}
			break
		}
	}
}

func startUDPServer(ctx context.Context, addr string, eng *engine.Engine, tag string) {
	lAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatalf("%s Resolve error: %v", tag, err)
	}

	conn, err := net.ListenUDP("udp", lAddr)
	if err != nil {
		log.Fatalf("%s Listen error: %v", tag, err)
	}
	defer conn.Close()

	log.Printf("%s Ingestion Server listening on %s (64KB buffer)\n", tag, addr)

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
