// Package config loads and validates the ringmaster.yaml project registry and
// provides placeholder interpolation for task argv strings.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root of a ringmaster.yaml file.
type Config struct {
	// Version is the config schema version. Only version 1 is supported.
	Version int `yaml:"version"`
	// PHPRoots lists directories scanned for php-<ver> folders.
	PHPRoots []string `yaml:"php_roots,omitempty"`
	// Defaults holds fallback values applied when a project or task omits them.
	Defaults Defaults `yaml:"defaults,omitempty"`
	// Projects is the registry of supervised projects.
	Projects []Project `yaml:"projects"`
}

// Defaults holds config-wide fallback values.
type Defaults struct {
	// Host is the default bind host (e.g. "127.0.0.1"), or "lan" for this
	// machine's current LAN IPv4, detected fresh each time. Use BindHost.
	Host string `yaml:"host,omitempty"`
	// PortRange is the inclusive [low, high] range used to allocate a free
	// port when a project does not declare one.
	PortRange []int `yaml:"port_range,omitempty"`
}

// Project describes one supervised project.
type Project struct {
	// ID is the unique slug used as the CLI key.
	ID string `yaml:"id"`
	// Label is a human-friendly display name.
	Label string `yaml:"label,omitempty"`
	// Path is the project's working directory.
	Path string `yaml:"path"`
	// PHP is a version prefix (e.g. "8.2") matched against discovered PHP versions.
	PHP string `yaml:"php,omitempty"`
	// Port is the default port for the primary task. Zero means "allocate from
	// defaults.port_range".
	Port int `yaml:"port,omitempty"`
	// Tasks are the processes ringmaster supervises for this project.
	Tasks []Task `yaml:"tasks"`
}

// Task describes one long-running process of a project.
type Task struct {
	// ID is the task key, unique within its project (e.g. "serve").
	ID string `yaml:"id"`
	// Argv is the command line, spawned with shell:false. The first element is
	// the executable; placeholders {php} {host} {port} are interpolated.
	Argv []string `yaml:"argv"`
	// Primary marks the task whose port is checked before starting.
	Primary bool `yaml:"primary,omitempty"`
	// Autostart controls whether `ringmaster up <project>` starts this task.
	// Nil means true (start by default).
	Autostart *bool `yaml:"autostart,omitempty"`
	// Restart is the restart policy: "" (treated as "no"), "no", or "on-failure".
	Restart string `yaml:"restart,omitempty"`
	// Ready is the optional readiness probe. Nil means the task is considered
	// running as soon as it is spawned.
	Ready *Ready `yaml:"ready,omitempty"`
	// External marks a service ringmaster observes but does not manage (a Windows
	// service, system Apache, a Docker container). No argv is spawned; status
	// is derived by probing Port (or the project's port when Port is zero).
	// External tasks cannot be primary and take no restart/ready policy.
	External bool `yaml:"external,omitempty"`
	// Port overrides the probed port for an external task (e.g. a Redis
	// server on 6379 attached to a project whose primary port is 8000).
	Port int `yaml:"port,omitempty"`
	// RequiresDocker makes starting this task fail fast with a clear message
	// when the Docker engine is not reachable (preflight `docker version`).
	RequiresDocker bool `yaml:"requires_docker,omitempty"`
	// Cleanup lists files, relative to the project path, that ringmaster deletes
	// before the task starts and after it exits. For tools that leave a
	// marker behind when killed instead of exiting cleanly, e.g. Vite's
	// public/hot, which otherwise points Laravel at a dead dev server.
	Cleanup []string `yaml:"cleanup,omitempty"`
}

// AutostartEnabled reports whether the task should start on `ringmaster up`
// (defaults to true when unset).
func (t Task) AutostartEnabled() bool {
	return t.Autostart == nil || *t.Autostart
}

// Ready describes a task readiness probe.
type Ready struct {
	// Type is "port" (poll TCP connect) or "stdout" (scan output for Match).
	Type string `yaml:"type"`
	// Match is the substring searched for when Type is "stdout".
	Match string `yaml:"match,omitempty"`
}

// Save validates cfg and writes it back to path as YAML. NOTE: comments and
// formatting of a hand-edited file are not preserved — the file is fully
// regenerated. The write goes through a temp file + rename so a crash cannot
// truncate the config.
func Save(path string, cfg *Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: replace %s: %w", path, err)
	}
	return nil
}

// Load reads, parses, and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config: %s not found — run: copy ringmaster.example.yaml ringmaster.yaml (then edit paths), or pass -config", path)
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses and validates raw YAML config bytes.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse yaml: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
