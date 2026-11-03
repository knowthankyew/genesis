package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStorage_AppendAndReplay(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	entries := [][]byte{
		[]byte("1730000001|auth-api|INFO|tr-1|First entry"),
		[]byte("1730000002|auth-api|WARN|tr-2|Second entry"),
		[]byte("1730000003|billing|ERROR|tr-3|Third entry"),
	}

	for _, e := range entries {
		if err := store.Append(e); err != nil {
			t.Fatalf("failed to append: %v", err)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("failed to flush: %v", err)
	}
	_ = store.Close()

	// Re-open and verify replay
	store2, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer store2.Close()

	var replayed [][]byte
	err = store2.Replay(func(raw []byte) {
		dup := make([]byte, len(raw))
		copy(dup, raw)
		replayed = append(replayed, dup)
	})
	if err != nil {
		t.Fatalf("replay error: %v", err)
	}

	if len(replayed) != len(entries) {
		t.Fatalf("expected %d replayed entries, got %d", len(entries), len(replayed))
	}

	for i := range entries {
		if !bytes.Equal(replayed[i], entries[i]) {
			t.Errorf("entry %d mismatch:\ngot:  %s\nwant: %s", i, replayed[i], entries[i])
		}
	}
}

func TestStorage_CrashRecovery_TornTail(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	validEntry := []byte("1730000001|auth-api|INFO|tr-1|Valid entry before crash")
	if err := store.Append(validEntry); err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	_ = store.Close()

	// Simulate crash: corrupt active segment by appending 3 bytes of a torn write
	segPath := filepath.Join(tmpDir, "00000001.seg")
	f, err := os.OpenFile(segPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open segment for crash simulation: %v", err)
	}
	// Append incomplete 3-byte header
	_, _ = f.Write([]byte{0x00, 0x00, 0x12})
	_ = f.Close()

	// Reopen store after crash
	store2, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to reopen store after crash: %v", err)
	}
	defer store2.Close()

	var replayed [][]byte
	err = store2.Replay(func(raw []byte) {
		replayed = append(replayed, raw)
	})
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}

	// Should successfully recover the 1 valid entry and safely discard torn tail
	if len(replayed) != 1 {
		t.Fatalf("expected 1 recovered entry, got %d", len(replayed))
	}
	if !bytes.Equal(replayed[0], validEntry) {
		t.Errorf("recovered entry mismatch: got %q, want %q", replayed[0], validEntry)
	}
}

func TestStorage_Burn(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	for i := 0; i < 10; i++ {
		_ = store.Append([]byte(fmt.Sprintf("log line %d", i)))
	}
	_ = store.Flush()

	if err := store.Burn(); err != nil {
		t.Fatalf("failed to burn storage: %v", err)
	}

	// Replay should find 0 entries
	var replayedCount int
	_ = store.Replay(func(raw []byte) {
		replayedCount++
	})
	if replayedCount != 0 {
		t.Errorf("expected 0 entries after burn, got %d", replayedCount)
	}

	_ = store.Close()
}
