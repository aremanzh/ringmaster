package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateUserDirs points every platform's per-user config folder at a temp
// folder and returns it.
func isolateUserDirs(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	return home
}

func TestResolvePath(t *testing.T) {
	home := isolateUserDirs(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	// -config always wins, existing or not.
	if got, err := ResolvePath(filepath.Join("some", "where.yaml")); err != nil || got != filepath.Join("some", "where.yaml") {
		t.Fatalf("explicit: got %q, %v", got, err)
	}

	// Nothing anywhere: the per-user folder, even though the file is not
	// there yet.
	got, err := ResolvePath("")
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if !strings.HasPrefix(got, home) || filepath.Base(got) != FileName ||
		!strings.EqualFold(filepath.Base(filepath.Dir(got)), "ringmaster") {
		t.Fatalf("fallback = %q, want <per-user config>/ringmaster/%s under %s", got, FileName, home)
	}

	// A ringmaster.yaml in the current folder wins over the per-user one, as
	// before 0.2.
	if err := os.MkdirAll(filepath.Dir(got), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(got, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, FileName), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePath(""); err != nil || got != FileName {
		t.Fatalf("current folder: got %q, %v, want %q", got, err, FileName)
	}

	// A folder with that name is not a config file.
	if err := os.Remove(filepath.Join(cwd, FileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cwd, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolvePath(""); err != nil || got == FileName {
		t.Fatalf("folder named like the config: got %q, %v", got, err)
	}
}

func TestLoadOrEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", FileName)

	cfg, existed, err := LoadOrEmpty(path)
	if err != nil || existed {
		t.Fatalf("missing file: existed %v, err %v", existed, err)
	}
	if cfg.Version != 1 || len(cfg.Projects) != 0 {
		t.Fatalf("empty config = %+v", cfg)
	}

	// The empty config saves (creating the folder) and loads back.
	cfg.PHPRoots = []string{"C:\\php"}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("first save: %v", err)
	}
	cfg, existed, err = LoadOrEmpty(path)
	if err != nil || !existed || len(cfg.PHPRoots) != 1 {
		t.Fatalf("after save: %+v, existed %v, err %v", cfg, existed, err)
	}

	// A broken file is still an error, not an empty config.
	if err := os.WriteFile(path, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrEmpty(path); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("invalid file: got %v", err)
	}
}
