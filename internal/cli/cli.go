package cli

import (
	"fmt"
	"os"
)

// Execute routes CLI subcommands or defaults to server mode
func Execute(args []string) error {
	if len(args) == 0 {
		return RunServer([]string{})
	}

	cmd := args[0]
	// If first argument is a flag (e.g. -tcp=...), default to server mode for backwards compatibility
	if len(cmd) > 0 && cmd[0] == '-' {
		return RunServer(args)
	}

	switch cmd {
	case "server":
		return RunServer(args[1:])
	case "run":
		return RunProcess(args[1:])
	case "pipe":
		return RunPipe(args[1:])
	case "query":
		return RunQuery(args[1:])
	case "tail":
		return RunTail(args[1:])
	case "stats":
		return RunStats(args[1:])
	case "burn":
		return RunBurn(args[1:])
	case "help", "-h", "--help":
		PrintUsage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		PrintUsage()
		os.Exit(1)
		return nil
	}
}

func PrintUsage() {
	fmt.Println(`Genesis - High-Performance In-Memory Telemetry Engine

Usage:
  genesis [command] [flags]

Commands:
  server    Start the Genesis telemetry server (HTTP, TCP, UDP, Syslog)
  run       Run an arbitrary child process, streaming its output to Genesis
  pipe      Pipe stdin lines directly into Genesis
  query     Query logs by time-range, level, service, or grep keyword
  tail      Live-tail streaming logs via Server-Sent Events (SSE)
  stats     Display live pipeline and memory statistics
  burn      Purge all logs from memory (Amnesiac Purge)
  help      Show help for Genesis

Run 'genesis [command] --help' for more information about a command.`)
}
