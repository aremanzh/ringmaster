package supervisor

import (
	"fmt"
	"os"
)

// This file exposes the small state-file API the CLI needs to operate
// cross-process: `byob up` runs the supervisor in the foreground, while
// `byob down` / `byob ps` from another terminal work purely off
// byob.state.json plus identity verification.

// EntryStatus classifies a recorded state entry against the live system.
type EntryStatus string

// Entry health verdicts.
const (
	// EntryDead: the recorded PID is not running.
	EntryDead EntryStatus = "dead"
	// EntryOurs: the PID is running and its image + start time match the
	// record — it really is the recorded process.
	EntryOurs EntryStatus = "ours"
	// EntryRecycled: the PID is running but does NOT match the record; the
	// OS recycled the PID. Never kill these.
	EntryRecycled EntryStatus = "recycled"
)

// LoadState reads the state file at path (missing file → empty map).
func LoadState(path string) (map[string]StateEntry, error) {
	return newStateFile(path).Load()
}

// AddStateEntry records e in the state file at path.
func AddStateEntry(path string, e StateEntry) error {
	return newStateFile(path).Add(e)
}

// RemoveStateEntry deletes key from the state file at path.
func RemoveStateEntry(path, key string) error {
	return newStateFile(path).Remove(key)
}

// SelfEntry builds a StateEntry describing the current process, used by
// `byob up` to record its own supervisor PID so `byob down` can stop it
// (its Job Object handles close on death, killing every task tree).
func SelfEntry(key string) (StateEntry, error) {
	image, created, err := processIdentity(os.Getpid())
	if err != nil {
		return StateEntry{}, fmt.Errorf("supervisor: identify self: %w", err)
	}
	return StateEntry{Key: key, PID: os.Getpid(), StartedAt: created, Image: image}, nil
}

// CheckEntry reports the health of a recorded entry.
func CheckEntry(e StateEntry) EntryStatus {
	if !pidAlive(e.PID) {
		return EntryDead
	}
	image, created, err := processIdentity(e.PID)
	if err == nil && identityMatches(e, image, created) {
		return EntryOurs
	}
	return EntryRecycled
}

// KillVerified terminates e's PID only after re-verifying identity. It
// returns true if a process was actually killed; a dead or recycled PID is
// left alone (false, nil).
func KillVerified(e StateEntry) (bool, error) {
	if CheckEntry(e) != EntryOurs {
		return false, nil
	}
	if err := killPID(e.PID); err != nil {
		return false, err
	}
	return true, nil
}
