package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// taskEnv returns the environment a task is spawned with: this process's
// environment with the task's own variables applied on top and prepend put in
// front of PATH. It returns nil, meaning "inherit unchanged", when the task
// asks for neither.
func taskEnv(overrides map[string]string, prepend []string) []string {
	if len(overrides) == 0 && len(prepend) == 0 {
		return nil
	}
	return mergeEnv(os.Environ(), overrides, prepend, runtime.GOOS == "windows", string(os.PathListSeparator))
}

// mergeEnv applies overrides to base ("KEY=value" entries) and then puts
// prepend in front of PATH, joined by sep. With foldCase, variable names
// compare case-insensitively, as on Windows, where PATH is usually spelled
// "Path"; an existing variable keeps its spelling. The order of base is kept
// and new variables are appended in sorted order.
func mergeEnv(base []string, overrides map[string]string, prepend []string, foldCase bool, sep string) []string {
	norm := func(k string) string {
		if foldCase {
			return strings.ToUpper(k)
		}
		return k
	}
	out := make([]string, 0, len(base)+len(overrides)+1)
	index := map[string]int{} // normalized name → position in out
	for _, kv := range base {
		// Windows keeps per-drive entries like "=C:=C:\dir": the name starts
		// with "=", so look for the separator after the first byte.
		if i := strings.Index(kv[min(1, len(kv)):], "="); i >= 0 {
			index[norm(kv[:i+1])] = len(out)
		}
		out = append(out, kv)
	}
	set := func(name, value string) {
		if i, ok := index[norm(name)]; ok {
			existing, _, _ := strings.Cut(out[i][min(1, len(out[i])):], "=")
			out[i] = out[i][:min(1, len(out[i]))] + existing + "=" + value
			return
		}
		index[norm(name)] = len(out)
		out = append(out, name+"="+value)
	}
	get := func(name string) string {
		if i, ok := index[norm(name)]; ok {
			_, v, _ := strings.Cut(out[i][min(1, len(out[i])):], "=")
			return v
		}
		return ""
	}

	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		set(name, overrides[name])
	}
	if len(prepend) > 0 {
		path := strings.Join(prepend, sep)
		if rest := get("PATH"); rest != "" {
			path += sep + rest
		}
		set("PATH", path)
	}
	return out
}

// resolveExe finds the executable for argv[0]. A bare name is looked up in
// dirs first, then on this process's PATH, so it resolves the same way it
// will inside the task, whose PATH starts with dirs.
func resolveExe(name string, dirs []string) (string, error) {
	if filepath.Base(name) == name {
		for _, dir := range dirs {
			if exe, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
				return exe, nil
			}
		}
	}
	return exec.LookPath(name)
}

// CommandArgv prepares argv for a one-off command the way a task's is: a
// bare argv[0] is looked up in dirs first, and a .cmd/.bat script is run
// through cmd.exe.
func CommandArgv(argv []string, dirs []string) ([]string, error) {
	return wrapArgvIn(argv, dirs)
}

// CommandEnv returns the environment for a one-off command: this process's
// environment with prepend in front of PATH, or nil (inherit) without one.
func CommandEnv(prepend []string) []string {
	return taskEnv(nil, prepend)
}
