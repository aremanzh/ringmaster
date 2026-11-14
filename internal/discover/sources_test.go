package discover

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestMain lets the test binary stand in for a PHP binary: copied into a
// folder as php(.exe) and run with RINGMASTER_FAKE_PHP set, it answers
// `php -v` with that version and exits. A real fake is needed because the
// version of an unversioned folder comes from running the binary, and a
// script cannot be named php.exe on Windows.
func TestMain(m *testing.M) {
	if v := os.Getenv("RINGMASTER_FAKE_PHP"); v != "" {
		fmt.Printf("PHP %s (cli) (built: Jan  1 2026 00:00:00) (NTS)\n", v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakePHP copies the test binary to dir as the platform's PHP binary.
func fakePHP(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dst := filepath.Join(dir, phpBinary())
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestScanRootsUnversionedFolders covers the layouts whose folder names do
// not carry the version: Herd (php84), Scoop (8.4.9, current), Homebrew
// (php@8.4/bin) and XAMPP (the binary in the root itself).
func TestScanRootsUnversionedFolders(t *testing.T) {
	t.Setenv("RINGMASTER_FAKE_PHP", "8.4.9")
	root := t.TempDir()
	want := map[string]string{ // path → version
		fakePHP(t, root):                                    "8.4.9",  // XAMPP style
		fakePHP(t, filepath.Join(root, "php84")):            "8.4.9",  // Herd style
		fakePHP(t, filepath.Join(root, "current")):          "8.4.9",  // Scoop style
		fakePHP(t, filepath.Join(root, "php@8.4", "bin")):   "8.4.9",  // Homebrew style
		fakePHP(t, filepath.Join(root, "php-8.2.27-Win32")): "8.2.27", // named: the name wins, nothing is run
	}
	// Folders that must not turn into installs.
	if err := os.MkdirAll(filepath.Join(root, "ext"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", phpBinary()), []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}

	installs, err := scanRoots([]string{root}, "root", true)
	if err != nil {
		t.Fatalf("scanRoots: %v", err)
	}
	got := map[string]string{}
	for _, in := range installs {
		got[in.Path] = in.Version
	}
	if len(got) != len(want) {
		t.Fatalf("got %d installs, want %d: %v", len(got), len(want), got)
	}
	for path, version := range want {
		if got[path] != version {
			t.Errorf("%s: version %q, want %q", path, got[path], version)
		}
	}
}

// TestDefaultFoldersOnlyWithoutRoots: the well-known folders are a first-run
// convenience and stay out once php_roots is configured.
func TestDefaultFoldersOnlyWithoutRoots(t *testing.T) {
	if n := len(Sources(nil)); n != 3 {
		t.Errorf("no php_roots: %d sources, want 3 (roots, PATH, default folders)", n)
	}
	if n := len(Sources([]string{t.TempDir()})); n != 2 {
		t.Errorf("with php_roots: %d sources, want 2 (roots, PATH)", n)
	}
}

// TestScanRootsLenientSkipsUnreadable: a default folder that is not a folder
// is skipped; the same thing in php_roots is reported.
func TestScanRootsLenientSkipsUnreadable(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if installs, err := scanRoots([]string{notADir}, "default", false); err != nil || len(installs) != 0 {
		t.Errorf("lenient: got %v, %v", installs, err)
	}
	if _, err := scanRoots([]string{notADir}, "root", true); err == nil {
		t.Error("strict: an unreadable php root was not reported")
	}
}

func TestVersionOfIsCached(t *testing.T) {
	t.Setenv("RINGMASTER_FAKE_PHP", "8.3.1")
	exe := fakePHP(t, t.TempDir())
	if v := versionOf(exe); v != "8.3.1" {
		t.Fatalf("versionOf = %q, want 8.3.1", v)
	}
	// Same file, different answer available: the cached one is used.
	t.Setenv("RINGMASTER_FAKE_PHP", "9.9.9")
	if v := versionOf(exe); v != "8.3.1" {
		t.Fatalf("second versionOf = %q, want the cached 8.3.1", v)
	}
}
