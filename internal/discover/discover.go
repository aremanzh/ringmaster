// Package discover locates PHP binaries on the machine and resolves a
// project's version constraint (e.g. "8.2") to a concrete php.exe path.
package discover

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
	// Source names where this install was found (e.g. "root:C:\laragon\bin\php", "PATH").
	Source string
}

// Source is one discovery strategy. Each returns the installs it can find;
// errors are reserved for real failures (an unreadable configured root), not
// for "nothing found here".
type Source func() ([]Install, error)

// Sources returns the M1 discovery strategies in scan order: configured
// php_roots first, then PATH.
//
// TODO(M3): add sources for Laravel Herd, XAMPP, scoop, and Homebrew here —
// each is just another Source func; the resolver does not change.
func Sources(phpRoots []string) []Source {
	return []Source{
		func() ([]Install, error) { return scanRoots(phpRoots) },
		scanPATH,
	}
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

// phpDirPattern matches version-carrying folder names like
// "php-8.2.27-nts-Win32-vs16-x64" or plain "php-8.2.27".
var phpDirPattern = regexp.MustCompile(`^php-(\d+\.\d+(?:\.\d+)?)`)

// scanRoots walks each configured root looking for php-<ver>* subfolders
// containing php.exe.
func scanRoots(roots []string) ([]Install, error) {
	var installs []Install
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				// A configured root that does not exist is not fatal; the
				// resolver will report "no matching version" with context.
				continue
			}
			return nil, fmt.Errorf("discover: read php root %s: %w", root, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			m := phpDirPattern.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			exe := filepath.Join(root, e.Name(), phpBinary())
			if _, err := os.Stat(exe); err != nil {
				// Version-manager layouts (Homebrew cellar, phpenv) nest the
				// binary under bin/.
				exe = filepath.Join(root, e.Name(), "bin", phpBinary())
				if _, err := os.Stat(exe); err != nil {
					continue
				}
			}
			installs = append(installs, Install{
				Version: m[1],
				Path:    exe,
				Source:  "root:" + root,
			})
		}
	}
	return installs, nil
}

// phpVersionOutput matches the first line of `php -v`, e.g.
// "PHP 8.2.27 (cli) (built: ...)".
var phpVersionOutput = regexp.MustCompile(`^PHP (\d+\.\d+\.\d+)`)

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
	out, err := exec.Command(abs, "-v").Output()
	if err != nil {
		// A php.exe that cannot even print its version is unusable; skip it
		// rather than failing all discovery.
		return nil, nil
	}
	m := phpVersionOutput.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return nil, nil
	}
	return []Install{{Version: m[1], Path: abs, Source: "PATH"}}, nil
}
