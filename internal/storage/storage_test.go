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

func TestStorage_PruneMaxBytes(t *testing.T) {
	tmpDir := t.TempDir()

	// Write 3 inactive segment files and 1 active segment file
	for i := 1; i <= 3; i++ {
		segPath := filepath.Join(tmpDir, fmt.Sprintf("%08d.seg", i))
		if err := os.WriteFile(segPath, bytes.Repeat([]byte("A"), 1024), 0644); err != nil {
			t.Fatalf("failed to write test segment %d: %v", i, err)
		}
	}

	store, err := Open(tmpDir)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Append to active segment (00000004.seg)
	_ = store.Append(bytes.Repeat([]byte("B"), 500))
	_ = store.Flush()

	// Total size is ~3572 bytes. Prune to maxBytes = 2000 bytes.
	// Should delete 00000001.seg and 00000002.seg, keeping 00000003.seg (1024B) + 00000004.seg (~512B) <= 2000B.
	if err := store.Prune(0, 2000); err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	entries, _ := os.ReadDir(tmpDir)
	var segs []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".seg" {
			segs = append(segs, e.Name())
		}
	}

	if len(segs) != 2 {
		t.Fatalf("expected 2 segments remaining after maxBytes prune, got %d (%v)", len(segs), segs)
	}
	if segs[0] != "00000003.seg" || segs[1] != "00000004.seg" {
		t.Errorf("unexpected remaining segments: %v", segs)
	}
}
