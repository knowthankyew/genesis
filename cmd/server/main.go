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
	"sync"
	"syscall"
	"time"

	"project-genesis/internal/engine"
)

var (
	tcpAddr = flag.String("tcp", ":9000", "TCP ingestion address")
	udpAddr = flag.String("udp", ":9001", "UDP ingestion address")
	httpAddr = flag.String("http", ":8080", "HTTP query API address")
	maxMemMB = flag.Int64("max-mem-mb", 512, "Maximum in-memory index size in MB")
)

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 4096)
		return &b
	},
}

func main() {
	flag.Parse()

	log.Println("==========================================================")
	log.Println("  PROJECT GENESIS: High-Performance Distributed Log Engine")
	log.Println("==========================================================")

	eng := engine.NewEngine(engine.EngineConfig{
		MaxMemoryBytes:   *maxMemMB * 1024 * 1024,
		QueueCapacity:    200000,
		PriorityQueueCap: 50000,
	})
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Start HTTP Query Server
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/query", eng.HandleQuery)
	httpMux.HandleFunc("/stats", eng.HandleStats)

	httpServer := &http.Server{
		Addr:    *httpAddr,
		Handler: httpMux,
	}

	go func() {
		log.Printf("[HTTP] Query Server listening on %s\n", *httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[HTTP] Error: %v", err)
		}
	}()

	// 2. Start TCP Listener
	go startTCPServer(ctx, *tcpAddr, eng)

	// 3. Start UDP Listener
	go startUDPServer(ctx, *udpAddr, eng)

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("\n[Shutdown] Shutting down Project Genesis...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Println("[Shutdown] Complete.")
}

func startTCPServer(ctx context.Context, addr string, eng *engine.Engine) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[TCP] Listen error: %v", err)
	}
	defer listener.Close()

	log.Printf("[TCP] Ingestion Server listening on %s (epoll/kqueue multiplexed)\n", addr)

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

		go handleTCPConnection(conn, eng)
	}
}

func handleTCPConnection(conn net.Conn, eng *engine.Engine) {
	defer conn.Close()

	reader := bufio.NewReaderSize(conn, 32*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_ = eng.Ingest(line)
		}
		if err != nil {
			if err != io.EOF {
				// connection reset or closed
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

	log.Printf("[UDP] Ingestion Server listening on %s\n", addr)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)
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
