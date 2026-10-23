package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StateEntry records one spawned task in ringmaster.state.json, with enough
// identity (image + OS start time) to detect Windows PID recycling before
// ever killing a recorded PID.
type StateEntry struct {
	Key       string    `json:"key"` // "<projectId>:<taskId>"
	PID       int       `json:"pid"`
	Port      int       `json:"port,omitempty"`
	StartedAt time.Time `json:"started_at"` // OS process creation time
	Image     string    `json:"image"`      // executable name, e.g. "php.exe"
}

// stateFile persists StateEntry records with whole-file read-modify-write
// under a mutex. The file is machine-local runtime state (gitignored).
type stateFile struct {
	mu   sync.Mutex
	path string
}

func newStateFile(path string) *stateFile {
	return &stateFile{path: path}
}

// Load returns all recorded entries; a missing file is an empty state.
func (s *stateFile) Load() (map[string]StateEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// retryShare retries transient Windows file-sharing violations: during a
// daemon handover the outgoing process may hold the state file for a few
// milliseconds mid-rename while the successor reads it.
func retryShare(op func() error) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = op(); err == nil || os.IsNotExist(err) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return err
}

func (s *stateFile) loadLocked() (map[string]StateEntry, error) {
	var data []byte
	err := retryShare(func() error {
		var rerr error
		data, rerr = os.ReadFile(s.path)
		return rerr
	})
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]StateEntry{}, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", s.path, err)
	}
	var entries map[string]StateEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		// A corrupt state file must not brick every ringmaster command. Preserve it
		// for inspection and continue with an empty state: the worst case is
		// an orphan that `ringmaster down` can no longer identify, which the Job
		// Object layer already covers.
		fmt.Fprintf(os.Stderr, "ringmaster: state file %s is corrupt (%v) — moving it aside and starting fresh\n", s.path, err)
		_ = os.Rename(s.path, s.path+".corrupt")
		return map[string]StateEntry{}, nil
	}
	if entries == nil {
		entries = map[string]StateEntry{}
	}
	return entries, nil
}

// Add records (or replaces) an entry.
func (s *stateFile) Add(e StateEntry) error {
	return s.mutate(func(m map[string]StateEntry) { m[e.Key] = e })
}

// Remove deletes an entry; removing a missing key is not an error.
func (s *stateFile) Remove(key string) error {
	return s.mutate(func(m map[string]StateEntry) { delete(m, key) })
}

func (s *stateFile) mutate(f func(map[string]StateEntry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadLocked()
	if err != nil {
		return err
	}
	f(entries)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}
	// Atomic replace via a per-process temp file: concurrent writers (e.g. a
	// daemon shutting down while its successor starts) can lose the race but
	// can never interleave bytes into a corrupt file.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".ringmaster.state.*.tmp")
	if err != nil {
		return fmt.Errorf("state: temp file: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("state: write %s: %w", tmpName, errors.Join(werr, cerr))
	}
	if err := retryShare(func() error { return os.Rename(tmpName, s.path) }); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("state: replace %s: %w", s.path, err)
	}
	return nil
}
