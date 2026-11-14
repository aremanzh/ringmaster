package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// FileName is the config file's name wherever it lives.
const FileName = "ringmaster.yaml"

// ResolvePath returns the config file to use. explicit is the -config flag;
// when it is empty the first of these wins:
//
//  1. ringmaster.yaml in the current folder (how every setup before 0.2
//     worked, so those keep working unchanged),
//  2. ringmaster.yaml next to the executable (the extracted zip, started
//     from a shortcut or from PATH),
//  3. the per-user config folder, whether or not the file exists yet:
//     %APPDATA%\Ringmaster on Windows, ~/Library/Application Support/Ringmaster
//     on macOS, ~/.config/ringmaster elsewhere.
func ResolvePath(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if isFile(FileName) {
		return FileName, nil
	}
	if exe, err := os.Executable(); err == nil {
		if beside := filepath.Join(filepath.Dir(exe), FileName); isFile(beside) {
			return beside, nil
		}
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: no ringmaster.yaml here and no per-user config folder (%w): pass -config", err)
	}
	app := "ringmaster"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		app = "Ringmaster"
	}
	return filepath.Join(dir, app, FileName), nil
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// LoadOrEmpty is Load, except that a missing file is not an error: it yields
// an empty, valid config and existed == false. The file is then created by
// the first Save, i.e. when the first project or PHP folder is added.
func LoadOrEmpty(path string) (cfg *Config, existed bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Version: 1}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("config: read %s: %w", path, err)
	}
	cfg, err = Parse(data)
	return cfg, true, err
}
