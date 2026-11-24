package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDir(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"shop", "Blog", ".git", "api/src"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"artisan", "composer.json", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ListDir(root)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	var names []string
	for _, d := range got.Dirs {
		names = append(names, d.Name)
		if d.Path != filepath.Join(root, d.Name) {
			t.Errorf("%s: path %q", d.Name, d.Path)
		}
	}
	// Folders only, sorted without regard to case, hidden ones last.
	if strings.Join(names, ",") != "api,Blog,shop,.git" {
		t.Errorf("dirs = %v, want api, Blog, shop, .git", names)
	}
	if strings.Join(got.Markers, ",") != "artisan,composer.json" {
		t.Errorf("markers = %v, want artisan and composer.json", got.Markers)
	}
	if got.Path != root || got.Parent != filepath.Dir(root) || len(got.Roots) == 0 {
		t.Errorf("path %q parent %q roots %v", got.Path, got.Parent, got.Roots)
	}

	// The top of a drive or file system has no parent.
	top, err := ListDir(got.Roots[0])
	if err != nil {
		t.Fatalf("ListDir(%q): %v", got.Roots[0], err)
	}
	if top.Parent != "" {
		t.Errorf("root %q has parent %q", top.Path, top.Parent)
	}

	// No path means the home folder.
	if home, herr := os.UserHomeDir(); herr == nil {
		if def, err := ListDir(""); err != nil || def.Path != filepath.Clean(home) {
			t.Errorf("default listing: path %q, err %v, want the home folder %q", def.Path, err, home)
		}
	}

	for _, bad := range []string{filepath.Join(root, "missing"), filepath.Join(root, "notes.txt")} {
		_, err := ListDir(bad)
		ce, ok := err.(*Error)
		if !ok || ce.Kind != KindBadRequest {
			t.Errorf("ListDir(%q): err %v, want a bad-request error", bad, err)
		}
	}
}
