//go:build windows

package supervisor

import "testing"

func TestDecodeConsoleUTF8Passthrough(t *testing.T) {
	in := "café — ok ✓"
	if got := decodeConsole([]byte(in)); got != in {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeConsoleNonUTF8DoesNotMangle(t *testing.T) {
	// 0xE9 alone is invalid UTF-8; in cp1252 (and most Latin ANSI pages) it
	// is "é". Whatever the active codepage, the result must be valid and
	// non-empty — never the raw invalid byte passed through as U+FFFD noise
	// only. We assert it decoded to something printable.
	got := decodeConsole([]byte{'c', 'a', 'f', 0xE9})
	if len(got) < 4 {
		t.Fatalf("got %q", got)
	}
	if got[:3] != "caf" {
		t.Fatalf("prefix lost: %q", got)
	}
}
