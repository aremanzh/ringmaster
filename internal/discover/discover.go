// Package discover locates PHP binaries on the machine and resolves a
// project's version constraint (e.g. "8.2") to a concrete php.exe path.
package discover

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// phpBinary is the platform's PHP executable file name.
func phpBinary() string {
	if runtime.GOOS == "windows" {
		return "php.exe"
	}
	return "php"
}

// Install is one discovered PHP binary.
type Install struct {
	// Version is the full version string, e.g. "8.2.27".
	Version string
	// Path is the absolute path to php.exe.
	Path string
	// Source names where this install was found: "root:<php_roots entry>",
	// "PATH", or "default:<well-known folder>".
	Source string
}

// Source is one discovery strategy. Each returns the installs it can find;
// errors are reserved for real failures (an unreadable configured root), not
// for "nothing found here".
type Source func() ([]Install, error)

// Sources returns the discovery strategies in scan order: the configured
// php_roots, then PATH, then, only when no php_roots are configured, the
// default install folders of the common Windows and macOS PHP setups.
//
// The default folders are skipped once php_roots is set, so listing your PHP
// folders keeps resolution exactly as predictable as before: a project's
// "8.2" never silently moves to a copy of PHP that some other tool installed.
func Sources(phpRoots []string) []Source {
	srcs := []Source{
		func() ([]Install, error) { return scanRoots(phpRoots, "root", true) },
		scanPATH,
	}
	if len(phpRoots) == 0 {
		srcs = append(srcs, func() ([]Install, error) { return scanRoots(defaultRoots(), "default", false) })
	}
	return srcs
}

// Discover runs all sources and returns every install found, deduplicated by
// path (first source wins).
func Discover(phpRoots []string) ([]Install, error) {
	var all []Install
	seen := map[string]bool{}
	for _, src := range Sources(phpRoots) {
		installs, err := src()
		if err != nil {
			return nil, err
		}
		for _, in := range installs {
			key := strings.ToLower(in.Path)
			if !seen[key] {
				seen[key] = true
				all = append(all, in)
			}
		}
	}
	return all, nil
}

// defaultRoots lists where the usual PHP setups put their versions. Folders
// that do not exist are simply skipped by the scan.
func defaultRoots() []string {
	home, _ := os.UserHomeDir()
	var roots []string
	switch runtime.GOOS {
	case "windows":
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}
		roots = append(roots,
			filepath.Join(drive+`\`, "laragon", "bin", "php"), // php-8.2.27-nts-Win32-...\php.exe
			filepath.Join(drive+`\`, "xampp", "php"),          // php.exe directly
		)
		if home != "" {
			roots = append(roots, filepath.Join(home, ".config", "herd", "bin")) // php84\php.exe
		}
		scoop := os.Getenv("SCOOP")
		if scoop == "" && home != "" {
			scoop = filepath.Join(home, "scoop")
		}
		if scoop != "" {
			roots = append(roots, filepath.Join(scoop, "apps", "php")) // 8.3.1\php.exe, current\php.exe
		}
	case "darwin":
		// Homebrew: opt/php/bin/php, opt/php@8.2/bin/php.
		roots = append(roots, "/opt/homebrew/opt", "/usr/local/opt")
	}
	return roots
}

// phpDirPattern matches version-carrying folder names like
// "php-8.2.27-nts-Win32-vs16-x64" or plain "php-8.2.27".
var phpDirPattern = regexp.MustCompile(`^php-(\d+\.\d+(?:\.\d+)?)`)

// scanRoots looks for PHP installs in each root:
//
//   - a subfolder named php-<version>… holding the binary takes its version
//     from the name, without running anything (Laragon, the php.net zips);
//   - any other subfolder holding the binary, and the root itself if it holds
//     one, is asked for its version with `php -v` (Herd's php84, Scoop's
//     8.3.1 and current, XAMPP's php, Homebrew's php@8.2).
//
// The binary may sit in the folder or in its bin/ subfolder. label names the
// kind of root in Install.Source. With strict, a root that exists but cannot
// be read is an error; otherwise it is skipped.
func scanRoots(roots []string, label string, strict bool) ([]Install, error) {
	var installs []Install
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) || !strict {
				// A configured root that does not exist is not fatal; the
				// resolver will report "no matching version" with context.
				continue
			}
			return nil, fmt.Errorf("discover: read php root %s: %w", root, err)
		}
		source := label + ":" + root
		if exe := binaryIn(root); exe != "" {
			if v := versionOf(exe); v != "" {
				installs = append(installs, Install{Version: v, Path: exe, Source: source})
			}
		}
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			// Stat, not e.IsDir: Scoop's "current" is a junction/symlink.
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			exe := binaryIn(dir)
			if exe == "" {
				continue
			}
			version := ""
			if m := phpDirPattern.FindStringSubmatch(e.Name()); m != nil {
				version = m[1]
			} else {
				version = versionOf(exe)
			}
			if version == "" {
				continue // not a PHP that can report its version
			}
			installs = append(installs, Install{Version: version, Path: exe, Source: source})
		}
	}
	return installs, nil
}

// binaryIn returns the PHP binary in dir or dir/bin (version-manager layouts
// such as Homebrew nest it there), or "".
func binaryIn(dir string) string {
	for _, exe := range []string{filepath.Join(dir, phpBinary()), filepath.Join(dir, "bin", phpBinary())} {
		if fi, err := os.Stat(exe); err == nil && !fi.IsDir() {
			return exe
		}
	}
	return ""
}

// phpVersionOutput matches the first line of `php -v`, e.g.
// "PHP 8.2.27 (cli) (built: ...)".
var phpVersionOutput = regexp.MustCompile(`^PHP (\d+\.\d+\.\d+)`)

// versionCache remembers `php -v` answers per binary, keyed by path, size
// and modification time, so repeated discovery (every config edit, every
// `ringmaster run`) does not start each PHP again.
var versionCache sync.Map

// versionOf asks a PHP binary for its version. It returns "" for anything
// that does not answer like PHP within a few seconds.
func versionOf(exe string) string {
	fi, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	key := fmt.Sprintf("%s|%d|%d", exe, fi.Size(), fi.ModTime().UnixNano())
	if v, ok := versionCache.Load(key); ok {
		return v.(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	version := ""
	if out, err := exec.CommandContext(ctx, exe, "-v").Output(); err == nil {
		if m := phpVersionOutput.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
			version = m[1]
		}
	}
	versionCache.Store(key, version)
	return version
}

// scanPATH finds php.exe on PATH and asks it for its version via `php -v`.
func scanPATH() ([]Install, error) {
	exe, err := exec.LookPath("php")
	if err != nil {
		return nil, nil // no php on PATH is not an error
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return nil, fmt.Errorf("discover: resolve PATH php %s: %w", exe, err)
	}
	// A php.exe that cannot even print its version is unusable; skip it
	// rather than failing all discovery.
	version := versionOf(abs)
	if version == "" {
		return nil, nil
	}
	return []Install{{Version: version, Path: abs, Source: "PATH"}}, nil
}
