package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// RunProcess executes an arbitrary child command, multiplexing stdout/stderr to Genesis ingestion
func RunProcess(args []string) error {
	var cmdArgs []string
	var flagArgs []string

	// Look for "--" separating genesis run flags from child command
	splitIdx := -1
	for i, arg := range args {
		if arg == "--" {
			splitIdx = i
			break
		}
	}

	if splitIdx != -1 {
		flagArgs = args[:splitIdx]
		cmdArgs = args[splitIdx+1:]
	} else {
		// Look for first non-flag argument
		for i, arg := range args {
			if len(arg) > 0 && arg[0] != '-' {
				flagArgs = args[:i]
				cmdArgs = args[i:]
				break
			}
		}
	}

	if len(cmdArgs) == 0 {
		return fmt.Errorf("no command specified. Usage: genesis run [--service <name>] [--endpoint <host:port>] -- <command> [args...]")
	}

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	service := fs.String("service", "genesis-app", "Service name tag for ingested logs")
	endpoint := fs.String("endpoint", "127.0.0.1:9000", "Genesis TCP ingestion address")
	_ = fs.Parse(flagArgs)

	// Attempt connection to Genesis ingestion endpoint
	conn, err := net.DialTimeout("tcp", *endpoint, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[genesis run] Warning: unable to connect to Genesis at %s (%v). Running command without log streaming.\n", *endpoint, err)
	} else {
		defer conn.Close()
	}

	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to open stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command %q: %w", cmdArgs[0], err)
	}

	var wg sync.WaitGroup
	var connMu sync.Mutex

	streamer := func(r io.Reader, out io.Writer, defaultLvl string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Bytes()
			// 1. Output to local console
			_, _ = out.Write(append(line, '\n'))

			// 2. Send to Genesis
			if conn != nil {
				now := time.Now().Unix()
				// Send as Pipe format: <timestamp>|<service>|<level>|<trace_id>|<message>
				entryLine := fmt.Sprintf("%d|%s|%s||%s\n", now, *service, defaultLvl, string(line))
				connMu.Lock()
				_, _ = conn.Write([]byte(entryLine))
				connMu.Unlock()
			}
		}
	}

	wg.Add(2)
	go streamer(stdoutPipe, os.Stdout, "INFO")
	go streamer(stderrPipe, os.Stderr, "ERROR")

	wg.Wait()
	cmdErr := cmd.Wait()

	if exitErr, ok := cmdErr.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	} else if cmdErr != nil {
		return cmdErr
	}

	return nil
}
