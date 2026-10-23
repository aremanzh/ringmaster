//go:build windows

package proc

import (
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestKillTerminatesTree is the core contract test: spawn cmd.exe, which
// spawns a long-running ping child, then Kill() and assert BOTH processes
// are gone — not just the root.
func TestKillTerminatesTree(t *testing.T) {
	g := New()
	cmd := exec.Command("cmd.exe", "/c", "ping -n 60 127.0.0.1 >NUL")
	if err := g.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rootPID := uint32(g.PID())

	childPID := waitForChild(t, rootPID, 10*time.Second)
	if !pidAlive(rootPID) || !pidAlive(childPID) {
		t.Fatalf("expected root %d and child %d alive before Kill", rootPID, childPID)
	}

	if err := g.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitForDeath(t, rootPID, 5*time.Second, "root")
	waitForDeath(t, childPID, 5*time.Second, "child")

	select {
	case <-g.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done() not closed after Kill")
	}

	if err := g.Kill(); err != nil {
		t.Fatalf("second Kill not idempotent: %v", err)
	}
}

// TestDoneOnNaturalExit checks Done() closes when the process exits by itself.
func TestDoneOnNaturalExit(t *testing.T) {
	g := New()
	if err := g.Start(exec.Command("cmd.exe", "/c", "exit 0")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-g.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("Done() not closed after natural exit")
	}
	if err := g.Kill(); err != nil {
		t.Fatalf("Kill after natural exit: %v", err)
	}
}

// TestStartTwiceRejected guards the single-use contract.
func TestStartTwiceRejected(t *testing.T) {
	g := New()
	if err := g.Start(exec.Command("cmd.exe", "/c", "exit 0")); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer g.Kill()
	if err := g.Start(exec.Command("cmd.exe", "/c", "exit 0")); err == nil {
		t.Fatal("second Start should fail")
	}
}

// waitForChild polls the process snapshot until rootPID has a child.
func waitForChild(t *testing.T, rootPID uint32, timeout time.Duration) uint32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid := findChild(t, rootPID); pid != 0 {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no child of %d appeared within %s", rootPID, timeout)
	return 0
}

func findChild(t *testing.T, parent uint32) uint32 {
	t.Helper()
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatalf("process snapshot: %v", err)
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ParentProcessID == parent {
			return pe.ProcessID
		}
	}
	return 0
}

// pidAlive reports whether pid refers to a still-running process.
func pidAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

func waitForDeath(t *testing.T, pid uint32, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s pid %d still alive %s after Kill", label, pid, timeout)
}
