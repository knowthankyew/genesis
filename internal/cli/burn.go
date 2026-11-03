package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func RunBurn(args []string) error {
	fs := flag.NewFlagSet("burn", flag.ExitOnError)
	httpURL := fs.String("http", "http://127.0.0.1:8080", "Genesis HTTP address")
	force := fs.Bool("y", false, "Confirm burn without interactive prompt")
	fs.BoolVar(force, "force", false, "Confirm burn without interactive prompt")
	_ = fs.Parse(args)

	if !*force {
		fmt.Print("WARNING: This will purge all in-memory telemetry logs with zero disk trace.\nAre you sure you want to proceed? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			fmt.Println("Burn cancelled.")
			return nil
		}
	}

	baseURL := strings.TrimRight(*httpURL, "/")
	req, err := http.NewRequest(http.MethodDelete, baseURL+"/logs", nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("burn request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("burn failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Printf("[Amnesiac Purge] Success: %v\n", result["message"])
	return nil
}
