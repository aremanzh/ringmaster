package supervisor

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name      string
		base      []string
		overrides map[string]string
		prepend   []string
		foldCase  bool
		sep       string
		want      []string
	}{
		{
			name:      "override replaces in place, new variables are appended sorted",
			base:      []string{"HOME=/root", "APP_ENV=production", "PATH=/usr/bin"},
			overrides: map[string]string{"APP_ENV": "local", "ZED": "1", "ALPHA": "2"},
			sep:       ":",
			want:      []string{"HOME=/root", "APP_ENV=local", "PATH=/usr/bin", "ALPHA=2", "ZED=1"},
		},
		{
			name:    "prepend goes in front of PATH",
			base:    []string{"PATH=/usr/bin:/bin"},
			prepend: []string{"/opt/php-8.2/bin"},
			sep:     ":",
			want:    []string{"PATH=/opt/php-8.2/bin:/usr/bin:/bin"},
		},
		{
			name:     "Windows spells it Path and keeps that spelling",
			base:     []string{`=C:=C:\code\shop`, `Path=C:\Windows;C:\php-8.4`, "TEMP=x"},
			prepend:  []string{`C:\laragon\bin\php\php-8.2.27`},
			foldCase: true,
			sep:      ";",
			want:     []string{`=C:=C:\code\shop`, `Path=C:\laragon\bin\php\php-8.2.27;C:\Windows;C:\php-8.4`, "TEMP=x"},
		},
		{
			name:      "an env PATH is the base the prepend is added to",
			base:      []string{"PATH=/usr/bin"},
			overrides: map[string]string{"PATH": "/custom"},
			prepend:   []string{"/php"},
			sep:       ":",
			want:      []string{"PATH=/php:/custom"},
		},
		{
			name:      "case-insensitive override on Windows",
			base:      []string{"App_Env=production"},
			overrides: map[string]string{"APP_ENV": "local"},
			foldCase:  true,
			sep:       ";",
			want:      []string{"App_Env=local"},
		},
		{
			name:      "case-sensitive elsewhere",
			base:      []string{"App_Env=production"},
			overrides: map[string]string{"APP_ENV": "local"},
			sep:       ":",
			want:      []string{"App_Env=production", "APP_ENV=local"},
		},
		{
			name:    "no PATH at all",
			base:    []string{"HOME=/root"},
			prepend: []string{"/php"},
			sep:     ":",
			want:    []string{"HOME=/root", "PATH=/php"},
		},
		{
			name:      "values may contain equals signs",
			base:      []string{"OPTS=a=b"},
			overrides: map[string]string{"DSN": "mysql:host=db;port=3306"},
			sep:       ":",
			want:      []string{"OPTS=a=b", "DSN=mysql:host=db;port=3306"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeEnv(tt.base, tt.overrides, tt.prepend, tt.foldCase, tt.sep)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestTaskEnvInheritsWhenNothingSet(t *testing.T) {
	if env := taskEnv(nil, nil); env != nil {
		t.Fatalf("taskEnv(nil, nil) = %d entries, want nil (inherit)", len(env))
	}
}

// TestTaskGetsEnvCwdAndPath starts a real task and checks what it sees: its
// own env variable, its working directory, the prepended PATH folder, and
// that a bare argv[0] is found in that folder rather than on PATH.
func TestTaskGetsEnvCwdAndPath(t *testing.T) {
	// A private bin folder holding a copy of the test binary under a name
	// that exists nowhere on PATH.
	bin := t.TempDir()
	name := "ringmaster-fake-php"
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe" // argv[0] stays bare: the lookup adds the extension
	}
	copyFile(t, os.Args[0], filepath.Join(bin, exe))
	work := t.TempDir()

	argv := helperArgv(t, 0, 0)
	argv[0] = name
	s := newTestSupervisor(t)
	spec := TaskSpec{
		Key: "p:env", Dir: work, Argv: argv,
		Env:         map[string]string{"RINGMASTER_TEST_VALUE": "from-task-env"},
		PathPrepend: []string{bin},
	}
	if err := s.Start(spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, s, "p:env", StatusStopped, 20*time.Second)

	waitLogLines(t, s, "p:env", "helper value=from-task-env", 1, 5*time.Second)
	got := waitLogLines(t, s, "p:env", "helper cwd=", 1, 5*time.Second)[0]
	if !samePath(t, got[len("helper cwd="):], work) {
		t.Errorf("cwd: %q, want %q", got, work)
	}
	got = waitLogLines(t, s, "p:env", "helper path0=", 1, 5*time.Second)[0]
	if !samePath(t, got[len("helper path0="):], bin) {
		t.Errorf("first PATH entry: %q, want %q", got, bin)
	}

	// Without the folder the same bare name cannot be resolved.
	spec.Key, spec.PathPrepend = "p:nopath", nil
	if err := s.Start(spec); err == nil {
		t.Fatal("bare argv[0] resolved without its folder on the task PATH")
	}
}

// samePath compares two directories, resolving symlinks (macOS temp dirs)
// and letter case (Windows).
func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return false
	}
	ia, err1 := os.Stat(ra)
	ib, err2 := os.Stat(rb)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
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
}
