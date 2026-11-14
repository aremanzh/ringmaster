package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aremanzh/ringmaster/internal/config"
)

// TestHelperProcess is the child process of the run tests, not a real test:
// it reports where it runs and what leads its PATH, then exits with the code
// it was given.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RINGMASTER_TEST_HELPER") == "" {
		return
	}
	wd, _ := os.Getwd()
	fmt.Println("cwd=" + wd)
	fmt.Println("path0=" + filepath.SplitList(os.Getenv("PATH"))[0])
	code := 0
	fmt.Sscanf(os.Args[len(os.Args)-1], "%d", &code)
	os.Exit(code)
}

func testApp(t *testing.T, p config.Project, phpRoots ...string) *app {
	t.Helper()
	dir := t.TempDir()
	return &app{
		cfg:       &config.Config{Version: 1, PHPRoots: phpRoots, Projects: []config.Project{p}},
		cfgPath:   filepath.Join(dir, "ringmaster.yaml"),
		statePath: filepath.Join(dir, "ringmaster.state.json"),
	}
}

// fakePHPRoot creates <root>/php-<version>/ holding an (empty) PHP binary and
// returns the root and that folder.
func fakePHPRoot(t *testing.T, version string) (root, folder string) {
	t.Helper()
	root = t.TempDir()
	folder = filepath.Join(root, "php-"+version)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "php"
	if runtime.GOOS == "windows" {
		name = "php.exe"
	}
	if err := os.WriteFile(filepath.Join(folder, name), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, folder
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return false
	}
	ia, err1 := os.Stat(ra)
	ib, err2 := os.Stat(rb)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

func lineValue(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return v
		}
	}
	return ""
}

// TestRunCommand: the command runs in the project folder with the project's
// PHP folder first on PATH, and its exit code comes back as is.
func TestRunCommand(t *testing.T) {
	t.Setenv("RINGMASTER_TEST_HELPER", "1")
	root, phpFolder := fakePHPRoot(t, "8.2.27")
	projectDir := t.TempDir()
	a := testApp(t, config.Project{
		ID: "shop", Path: projectDir, PHP: "8.2",
		Tasks: []config.Task{{ID: "serve", Argv: []string{"{php}", "artisan", "serve"}}},
	}, root)
	helper := []string{os.Args[0], "-test.run=^TestHelperProcess$", "--"}

	var out, errOut bytes.Buffer
	err := a.runCommand(append([]string{"shop", "--"}, append(helper, "0")...), nil, &out, &errOut)
	if err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, errOut.String())
	}
	if got := lineValue(out.String(), "cwd="); !sameDir(got, projectDir) {
		t.Errorf("cwd = %q, want the project folder %q", got, projectDir)
	}
	if got := lineValue(out.String(), "path0="); !sameDir(got, phpFolder) {
		t.Errorf("first PATH entry = %q, want the project's PHP folder %q", got, phpFolder)
	}

	// The "--" is optional, and a non-zero exit code is passed through.
	out.Reset()
	err = a.runCommand(append([]string{"shop"}, append(helper, "7")...), nil, &out, &errOut)
	var code exitCode
	if !errors.As(err, &code) || code != 7 {
		t.Fatalf("exit code: got %v, want exitCode 7", err)
	}
}

func TestRunCommandErrors(t *testing.T) {
	root, _ := fakePHPRoot(t, "8.2.27")
	dir := t.TempDir()
	a := testApp(t, config.Project{ID: "shop", Path: dir, PHP: "8.4", Tasks: []config.Task{{ID: "x", Argv: []string{"x"}}}}, root)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no arguments", nil, "usage: ringmaster run"},
		{"no command", []string{"shop"}, "usage: ringmaster run"},
		{"only the separator", []string{"shop", "--"}, "usage: ringmaster run"},
		{"unknown project", []string{"nope", "php", "-v"}, `unknown project "nope"`},
		{"php version not installed", []string{"shop", "php", "-v"}, `no PHP install matches "8.4"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.runCommand(tt.args, nil, &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}

	// A command that does not exist is an error, not an exit code.
	a.cfg.Projects[0].PHP = "8.2"
	err := a.runCommand([]string{"shop", "ringmaster-no-such-command"}, nil, &bytes.Buffer{}, &bytes.Buffer{})
	var code exitCode
	if err == nil || errors.As(err, &code) {
		t.Fatalf("missing command: got %v, want a plain error", err)
	}
}

func TestLastLines(t *testing.T) {
	tests := []struct {
		data string
		n    int
		want string
	}{
		{"a\nb\nc\n", 2, "b\nc\n"},
		{"a\nb\nc", 2, "b\nc"},
		{"a\nb\nc\n", 3, "a\nb\nc\n"},
		{"a\nb\nc\n", 10, "a\nb\nc\n"},
		{"only\n", 1, "only\n"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		if got := string(lastLines([]byte(tt.data), tt.n)); got != tt.want {
			t.Errorf("lastLines(%q, %d) = %q, want %q", tt.data, tt.n, got, tt.want)
		}
	}
}
