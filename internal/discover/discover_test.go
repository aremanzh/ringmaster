package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	installs := []Install{
		{Version: "8.1.31", Path: `C:\php\8.1\php.exe`},
		{Version: "8.2.12", Path: `C:\php\8.2.12\php.exe`},
		{Version: "8.2.27", Path: `C:\php\8.2.27\php.exe`},
		{Version: "8.20.1", Path: `C:\php\8.20\php.exe`},
		{Version: "7.4.33", Path: `C:\php\7.4\php.exe`},
	}

	tests := []struct {
		name       string
		constraint string
		wantPath   string
		wantErr    string
	}{
		{name: "prefix picks highest patch", constraint: "8.2", wantPath: `C:\php\8.2.27\php.exe`},
		{name: "exact full version", constraint: "8.2.12", wantPath: `C:\php\8.2.12\php.exe`},
		{name: "major only picks highest", constraint: "8", wantPath: `C:\php\8.20\php.exe`},
		{name: "dot boundary excludes 8.20 from 8.2", constraint: "8.2", wantPath: `C:\php\8.2.27\php.exe`},
		{name: "old version still resolvable", constraint: "7.4", wantPath: `C:\php\7.4\php.exe`},
		{name: "no match fails clearly", constraint: "8.3", wantErr: `no PHP install matches "8.3"`},
		{name: "empty constraint fails", constraint: "", wantErr: "empty php version constraint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(installs, tt.constraint)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tt.wantPath)
			}
		})
	}
}

func TestResolveEmptyInstalls(t *testing.T) {
	_, err := Resolve(nil, "8.2")
	if err == nil || !strings.Contains(err.Error(), "found: none") {
		t.Fatalf("want 'found: none' error, got %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int // sign only
	}{
		{"8.2.27", "8.2.12", 1},
		{"8.2.12", "8.2.27", -1},
		{"8.2.27", "8.2.27", 0},
		{"8.2", "8.2.0", 0},
		{"8.10.0", "8.9.9", 1},
		{"7.4.33", "8.0.0", -1},
	}
	for _, tt := range tests {
		got := compareVersions(tt.a, tt.b)
		if sign(got) != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}

// TestScanRoots builds a fake php_roots tree on disk and checks that only
// version-carrying folders containing php.exe are picked up.
func TestScanRoots(t *testing.T) {
	root := t.TempDir()
	mk := func(dir string, withExe bool) {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if withExe {
			if err := os.WriteFile(filepath.Join(full, "php.exe"), []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("php-8.2.27-nts-Win32-vs16-x64", true)
	mk("php-8.1.31", true)
	mk("php-8.3.2-Win32-vs16-x64", false) // no php.exe → skipped
	mk("composer", true)                  // not a php-<ver> folder → skipped

	installs, err := scanRoots([]string{root, filepath.Join(root, "does-not-exist")})
	if err != nil {
		t.Fatalf("scanRoots: %v", err)
	}
	if len(installs) != 2 {
		t.Fatalf("got %d installs, want 2: %+v", len(installs), installs)
	}
	versions := map[string]bool{}
	for _, in := range installs {
		versions[in.Version] = true
		if _, err := os.Stat(in.Path); err != nil {
			t.Errorf("install path %s does not exist", in.Path)
		}
		if !strings.HasPrefix(in.Source, "root:") {
			t.Errorf("source = %q, want root:* prefix", in.Source)
		}
	}
	if !versions["8.2.27"] || !versions["8.1.31"] {
		t.Errorf("wrong versions: %v", versions)
	}
}

func TestDiscoverDeduplicates(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "php-8.2.27")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "php.exe"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Same root listed twice must not produce duplicate installs.
	installs, err := Discover([]string{root, root})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	count := 0
	for _, in := range installs {
		if strings.EqualFold(in.Path, filepath.Join(dir, "php.exe")) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("install listed %d times, want 1", count)
	}
}
