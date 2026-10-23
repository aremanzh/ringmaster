package portcheck

import (
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestFreeOnOpenPort(t *testing.T) {
	// Find a port that is definitely free by binding and releasing it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if err := Free("127.0.0.1", port); err != nil {
		t.Fatalf("Free(%d) = %v, want nil", port, err)
	}
}

func TestFreeOnBusyPortReportsHolder(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	err = Free("127.0.0.1", port)
	if err == nil {
		t.Fatalf("Free(%d) = nil, want busy error", port)
	}
	busy, ok := err.(*ErrPortBusy)
	if !ok {
		t.Fatalf("error type = %T, want *ErrPortBusy", err)
	}
	if runtime.GOOS == "windows" {
		if busy.Holder == nil {
			t.Fatal("holder not identified on windows")
		}
		if busy.Holder.PID != os.Getpid() {
			t.Errorf("holder PID = %d, want our own %d", busy.Holder.PID, os.Getpid())
		}
		if busy.Holder.Image == "" {
			t.Error("holder image empty")
		}
		if !strings.Contains(err.Error(), "in use by PID") {
			t.Errorf("error message %q lacks holder info", err)
		}
	}
}

func TestFreeInRange(t *testing.T) {
	// Occupy the low end of a range and check allocation skips it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port

	got, err := FreeInRange("127.0.0.1", busy, busy+20)
	if err != nil {
		t.Fatalf("FreeInRange: %v", err)
	}
	if got == busy {
		t.Errorf("allocated the busy port %d", busy)
	}
	if got < busy || got > busy+20 {
		t.Errorf("port %d outside range [%d, %d]", got, busy, busy+20)
	}
}

func TestFreeInRangeExcept(t *testing.T) {
	// Find a free anchor port, then exclude it: the next allocation must skip
	// it even though nothing is listening there yet.
	first, err := FreeInRange("127.0.0.1", 20000, 20040)
	if err != nil {
		t.Fatalf("FreeInRange: %v", err)
	}
	got, err := FreeInRangeExcept("127.0.0.1", 20000, 20040, map[int]bool{first: true})
	if err != nil {
		t.Fatalf("FreeInRangeExcept: %v", err)
	}
	if got == first {
		t.Errorf("allocated excluded port %d", first)
	}

	// A skip set covering the whole range must error, not loop or return 0.
	skip := map[int]bool{}
	for p := 20000; p <= 20005; p++ {
		skip[p] = true
	}
	if _, err := FreeInRangeExcept("127.0.0.1", 20000, 20005, skip); err == nil {
		t.Error("want error when every port in range is excluded")
	}
}
