package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
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

	"github.com/knowthankyew/genesis/internal/config"
	"github.com/knowthankyew/genesis/internal/engine"
	"github.com/knowthankyew/genesis/internal/redact"
	"github.com/knowthankyew/genesis/internal/security"
	"github.com/knowthankyew/genesis/internal/storage"
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
	// Pre-scan for -config
	var configPath string
	for i, arg := range args {
		if (arg == "-config" || arg == "--config") && i+1 < len(args) {
			configPath = args[i+1]
		} else if strings.HasPrefix(arg, "-config=") || strings.HasPrefix(arg, "--config=") {
			parts := strings.SplitN(arg, "=", 2)
			configPath = parts[1]
		}
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	fs.StringVar(&configPath, "config", configPath, "Path to genesis.json config file")
	tcpAddr := fs.String("tcp", cfg.TCPAddr, "TCP ingestion address")
	udpAddr := fs.String("udp", cfg.UDPAddr, "UDP ingestion address")
	syslogAddr := fs.String("syslog", cfg.SyslogAddr, "UDP Syslog ingestion address")
	httpAddr := fs.String("http", cfg.HTTPAddr, "HTTP query & UI API address")
	allowAllInterfaces := fs.Bool("allow-all-interfaces", cfg.AllowAllInterfaces, "Explicitly allow binding on all interfaces (0.0.0.0 / :port)")
	maxMemMB := fs.Int64("max-mem-mb", cfg.MaxMemMB, "Maximum in-memory index size in MB")
	maxConns := fs.Int64("max-conns", cfg.MaxConns, "Maximum simultaneous client TCP connections")
	maxLineLen := fs.Int("max-line-len", cfg.MaxLineLen, "Maximum log line length in bytes")
	dataDir := fs.String("data-dir", cfg.DataDir, "Directory for optional append-only disk persistence (default: empty, in-memory only)")
	redactEnabled := fs.Bool("redact", cfg.RedactEnabled, "Enable ingest-time PII and secret redaction")
	authToken := fs.String("auth-token", cfg.AuthToken, "Bearer token required for HTTP API & Web UI access")
	tlsCert := fs.String("tls-cert", cfg.TLSCertFile, "TLS certificate file path")
	tlsKey := fs.String("tls-key", cfg.TLSKeyFile, "TLS private key file path")

	_ = fs.Parse(args)

	// Update config with parsed flag overrides
	cfg.TCPAddr = *tcpAddr
	cfg.UDPAddr = *udpAddr
	cfg.SyslogAddr = *syslogAddr
	cfg.HTTPAddr = *httpAddr
	cfg.AllowAllInterfaces = *allowAllInterfaces
	cfg.MaxMemMB = *maxMemMB
	cfg.MaxConns = *maxConns
	cfg.MaxLineLen = *maxLineLen
	cfg.DataDir = *dataDir
	cfg.RedactEnabled = *redactEnabled
	cfg.AuthToken = *authToken
	cfg.TLSCertFile = *tlsCert
	cfg.TLSKeyFile = *tlsKey

	// Enforce 480MB memory ceiling on Go runtime GC so total heap never breaches 512MB
	debug.SetMemoryLimit(480 * 1024 * 1024)

	log.Println("==========================================================")
	log.Println("  GENESIS: High-Performance In-Memory Log Engine")
	log.Println("==========================================================")

	// Insecure bind check
	if IsBindingAllInterfaces(cfg.HTTPAddr) && !cfg.AllowAllInterfaces {
		log.Println("WARNING: Binding HTTP on all interfaces (:port or 0.0.0.0) without -allow-all-interfaces flag!")
		log.Println("Defaulting HTTP bind to 127.0.0.1:8080 for security. Use -allow-all-interfaces to override.")
		cfg.HTTPAddr = "127.0.0.1:8080"
	}

	// 1. Optional Ingest Redactor
	var redactor *redact.Redactor
	if cfg.RedactEnabled {
		var err error
		redactor, err = redact.NewRedactor(cfg.CustomRedact)
		if err != nil {
			return fmt.Errorf("failed to initialize redactor: %w", err)
		}
		log.Println("[Security] Ingest PII and credential redaction ACTIVE.")
	}

	// 2. Optional Disk Persistence
	var diskStore *storage.DiskStore
	if cfg.DataDir != "" {
		var err error
		diskStore, err = storage.Open(cfg.DataDir)
		if err != nil {
			return fmt.Errorf("failed to open storage in %q: %w", cfg.DataDir, err)
		}
		log.Printf("[Storage] Append-only disk persistence ACTIVE at %q (retention: %v, max: %d bytes)\n",
			cfg.DataDir, cfg.RetentionDuration, cfg.MaxDiskBytes)
	} else {
		log.Println("[Storage] Volatile In-Memory Mode (Zero Disk Trace).")
	}

	eng := engine.NewEngine(engine.EngineConfig{
		MaxMemoryBytes:   cfg.MaxMemMB * 1024 * 1024,
		QueueCapacity:    250000,
		PriorityQueueCap: 50000,
		MaxLineLength:    cfg.MaxLineLen,
		BucketDuration:   5, // 5-second time partitions
		Redactor:         redactor,
		Store:            diskStore,
	})
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Replay existing segments if persistence is active
	if diskStore != nil {
		var replayCount int
		_ = diskStore.Replay(func(raw []byte) {
			_ = eng.Ingest(raw)
			replayCount++
		})
		if replayCount > 0 {
			log.Printf("[Storage] Replayed %d historical records into in-memory index from disk.\n", replayCount)
		}

		// Background retention pruner
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = diskStore.Prune(cfg.RetentionDuration, cfg.MaxDiskBytes)
				}
			}
		}()
	}

	// 3. Setup HTTP Server (UI, Query, Stats, Tail SSE, Burn) with Auth Middleware
	httpMux := http.NewServeMux()
	httpMux.Handle("/", ui.Handler())
	httpMux.HandleFunc("/query", eng.HandleQuery)
	httpMux.HandleFunc("/stats", eng.HandleStats)
	httpMux.HandleFunc("/tail", eng.HandleTail)
	httpMux.HandleFunc("/logs", eng.HandleBurn)
	httpMux.HandleFunc("/burn", eng.HandleBurn)

	authenticatedHandler := security.AuthMiddleware(cfg.AuthToken, httpMux)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           authenticatedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	scheme := "http"
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		scheme = "https"
	}

	go func() {
		log.Printf("[HTTP] Web UI & API listening on %s://%s\n", scheme, cfg.HTTPAddr)
		if cfg.AuthToken != "" {
			log.Println("[Security] HTTP Bearer token authentication REQUIRED.")
		}
		var err error
		if scheme == "https" {
			err = httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("[HTTP] Error: %v", err)
		}
	}()

	// 4. Start TCP Ingestion Listener
	go startTCPServer(ctx, cfg.TCPAddr, eng, cfg.MaxConns)

	// 5. Start UDP Ingestion Listener
	go startUDPServer(ctx, cfg.UDPAddr, eng, "[UDP]")

	// 6. Start Syslog UDP Listener
	if cfg.SyslogAddr != "" {
		go startUDPServer(ctx, cfg.SyslogAddr, eng, "[Syslog UDP]")
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
