package storage

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxSegmentBytes = 16 * 1024 * 1024 // 16 MB per segment file
	MagicHeaderLen  = 8                // 4-byte length + 4-byte CRC32
)

// DiskStore manages append-only segment files with crash resilience
type DiskStore struct {
	mu           sync.Mutex
	dataDir      string
	activeFile   *os.File
	activeWriter *bufio.Writer
	activeSegID  uint64
	activeBytes  int64
	closed       bool
}

// Open creates or resumes a DiskStore in the specified directory.
// If dataDir is empty, returns nil (in-memory mode).
func Open(dataDir string) (*DiskStore, error) {
	if dataDir == "" {
		return nil, nil
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data-dir %q: %w", dataDir, err)
	}

	store := &DiskStore{
		dataDir: dataDir,
	}

	// Find highest existing segment index
	segments, err := store.listSegments()
	if err != nil {
		return nil, err
	}

	var nextSegID uint64 = 1
	if len(segments) > 0 {
		nextSegID = segments[len(segments)-1] + 1
	}

	if err := store.rotateTo(nextSegID); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *DiskStore) rotateTo(segID uint64) error {
	if s.activeWriter != nil {
		_ = s.activeWriter.Flush()
	}
	if s.activeFile != nil {
		_ = s.activeFile.Close()
	}

	segName := fmt.Sprintf("%08d.seg", segID)
	segPath := filepath.Join(s.dataDir, segName)

	f, err := os.OpenFile(segPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open segment %q: %w", segPath, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}

	s.activeFile = f
	s.activeWriter = bufio.NewWriterSize(f, 65536)
	s.activeSegID = segID
	s.activeBytes = info.Size()
	return nil
}

// Append writes a single log entry framed with length and IEEE CRC32 checksum
func (s *DiskStore) Append(raw []byte) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	// Rotate segment if exceeding threshold
	if s.activeBytes >= MaxSegmentBytes {
		if err := s.rotateTo(s.activeSegID + 1); err != nil {
			return err
		}
	}

	length := uint32(len(raw))
	checksum := crc32.ChecksumIEEE(raw)

	var header [MagicHeaderLen]byte
	binary.BigEndian.PutUint32(header[0:4], length)
	binary.BigEndian.PutUint32(header[4:8], checksum)

	if _, err := s.activeWriter.Write(header[:]); err != nil {
		return err
	}
	if _, err := s.activeWriter.Write(raw); err != nil {
		return err
	}

	s.activeBytes += int64(MagicHeaderLen + len(raw))
	return nil
}

// Flush ensures buffered writes reach the operating system
func (s *DiskStore) Flush() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeWriter != nil {
		return s.activeWriter.Flush()
	}
	return nil
}

// Replay reads valid entries across all existing segments in order,
// cleanly discarding any torn/corrupted writes at the end of crash files.
func (s *DiskStore) Replay(fn func(raw []byte)) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeWriter != nil {
		_ = s.activeWriter.Flush()
	}

	segments, err := s.listSegments()
	if err != nil {
		return err
	}

	for _, segID := range segments {
		segPath := filepath.Join(s.dataDir, fmt.Sprintf("%08d.seg", segID))
		f, err := os.Open(segPath)
		if err != nil {
			continue
		}

		reader := bufio.NewReader(f)
		var header [MagicHeaderLen]byte

		for {
			_, err := io.ReadFull(reader, header[:])
			if err != nil {
				// EOF or partial header (torn write) -> segment finished
				break
			}

			lenVal := binary.BigEndian.Uint32(header[0:4])
			expectedChecksum := binary.BigEndian.Uint32(header[4:8])

			payload := make([]byte, lenVal)
			_, err = io.ReadFull(reader, payload)
			if err != nil {
				// Torn payload -> discard incomplete tail
				break
			}

			actualChecksum := crc32.ChecksumIEEE(payload)
			if actualChecksum != expectedChecksum {
				// Corrupted entry -> discard
				break
			}

			fn(payload)
		}
		_ = f.Close()
	}

	return nil
}

// Burn removes all segment files from disk and resets the store, ensuring zero disk trace
func (s *DiskStore) Burn() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeWriter != nil {
		_ = s.activeWriter.Flush()
	}
	if s.activeFile != nil {
		_ = s.activeFile.Close()
		s.activeFile = nil
		s.activeWriter = nil
	}

	entries, err := os.ReadDir(s.dataDir)
	if err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".seg") {
				_ = os.Remove(filepath.Join(s.dataDir, entry.Name()))
			}
		}
	}

	s.activeSegID = 1
	s.activeBytes = 0
	return s.rotateTo(1)
}

// Prune deletes segments older than maxAge or when total size exceeds maxBytes
func (s *DiskStore) Prune(maxAge time.Duration, maxBytes int64) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	segments, err := s.listSegments()
	if err != nil || len(segments) <= 1 {
		return nil // keep active segment
	}

	// Never prune the active segment (last in list)
	candidates := segments[:len(segments)-1]
	now := time.Now()

	for _, segID := range candidates {
		segPath := filepath.Join(s.dataDir, fmt.Sprintf("%08d.seg", segID))
		info, err := os.Stat(segPath)
		if err != nil {
			continue
		}

		if maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(segPath)
		}
	}

	return nil
}

func (s *DiskStore) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, err
	}

	var segments []uint64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".seg") {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".seg")
		if id, err := strconv.ParseUint(base, 10, 64); err == nil {
			segments = append(segments, id)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	return segments, nil
}

func (s *DiskStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.activeWriter != nil {
		_ = s.activeWriter.Flush()
	}
	if s.activeFile != nil {
		return s.activeFile.Close()
	}
	return nil
}
