package config

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// validRestart lists accepted restart policies. An empty string is treated
// as "no".
var validRestart = map[string]bool{"": true, "no": true, "on-failure": true}

// validReadyType lists accepted readiness probe types.
var validReadyType = map[string]bool{"port": true, "stdout": true}

// idPattern constrains project and task ids to safe slug characters, since
// they are used as CLI keys and in the state file.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Validate checks the whole config for structural errors: unsupported
// version, duplicate project ids or ports, malformed tasks, and unknown
// placeholders in argv.
func (c *Config) Validate() error {
	if c.Version == 0 {
		return fmt.Errorf(`config: missing "version: 1" at the top of ringmaster.yaml — see ringmaster.example.yaml`)
	}
	if c.Version != 1 {
		return fmt.Errorf("config: unsupported version %d (want 1)", c.Version)
	}
	if pr := c.Defaults.PortRange; pr != nil {
		if len(pr) != 2 {
			return fmt.Errorf("config: defaults.port_range must have exactly 2 elements, got %d", len(pr))
		}
		if pr[0] < 1 || pr[1] > 65535 || pr[0] > pr[1] {
			return fmt.Errorf("config: defaults.port_range [%d, %d] is not a valid ascending port range", pr[0], pr[1])
		}
	}

	seenID := map[string]bool{}
	seenPort := map[int]string{}
	for i, p := range c.Projects {
		if p.ID == "" {
			return fmt.Errorf("config: projects[%d]: missing id", i)
		}
		if !idPattern.MatchString(p.ID) {
			return fmt.Errorf("config: project %q: id must match %s", p.ID, idPattern)
		}
		if seenID[p.ID] {
			return fmt.Errorf("config: duplicate project id %q", p.ID)
		}
		seenID[p.ID] = true

		if p.Path == "" {
			return fmt.Errorf("config: project %q: missing path", p.ID)
		}
		if p.Port != 0 {
			if p.Port < 1 || p.Port > 65535 {
				return fmt.Errorf("config: project %q: port %d out of range", p.ID, p.Port)
			}
			if other, dup := seenPort[p.Port]; dup {
				return fmt.Errorf("config: projects %q and %q both declare port %d", other, p.ID, p.Port)
			}
			seenPort[p.Port] = p.ID
		}

		if err := validateTasks(p); err != nil {
			return err
		}
	}
	return nil
}

func validateTasks(p Project) error {
	if len(p.Tasks) == 0 {
		return fmt.Errorf("config: project %q: no tasks defined", p.ID)
	}
	seenTask := map[string]bool{}
	primaries := 0
	for i, t := range p.Tasks {
		if t.ID == "" {
			return fmt.Errorf("config: project %q: tasks[%d]: missing id", p.ID, i)
		}
		if !idPattern.MatchString(t.ID) {
			return fmt.Errorf("config: project %q: task %q: id must match %s", p.ID, t.ID, idPattern)
		}
		if seenTask[t.ID] {
			return fmt.Errorf("config: project %q: duplicate task id %q", p.ID, t.ID)
		}
		seenTask[t.ID] = true

		if t.External {
			if len(t.Argv) != 0 || t.Primary || t.Restart != "" || t.Ready != nil || t.RequiresDocker || len(t.Cleanup) != 0 {
				return fmt.Errorf("config: project %q: task %q: external tasks take no argv/primary/restart/ready/requires_docker/cleanup", p.ID, t.ID)
			}
			if t.Port != 0 && (t.Port < 1 || t.Port > 65535) {
				return fmt.Errorf("config: project %q: task %q: port %d out of range", p.ID, t.ID, t.Port)
			}
			continue
		}
		if t.Port != 0 {
			return fmt.Errorf("config: project %q: task %q: port is only valid on external tasks (managed tasks use the project port)", p.ID, t.ID)
		}
		if len(t.Argv) == 0 {
			return fmt.Errorf("config: project %q: task %q: argv must be a non-empty array", p.ID, t.ID)
		}
		for _, arg := range t.Argv {
			if arg == "" {
				return fmt.Errorf("config: project %q: task %q: argv contains an empty string", p.ID, t.ID)
			}
			if err := checkPlaceholders(arg); err != nil {
				return fmt.Errorf("config: project %q: task %q: %w", p.ID, t.ID, err)
			}
		}

		// Cleanup deletes files, so it must never reach outside the project.
		for _, c := range t.Cleanup {
			if !filepath.IsLocal(c) {
				return fmt.Errorf("config: project %q: task %q: cleanup path %q must be relative to the project and stay inside it", p.ID, t.ID, c)
			}
		}

		if t.Primary {
			primaries++
		}
		if !validRestart[t.Restart] {
			return fmt.Errorf("config: project %q: task %q: invalid restart policy %q (want \"no\" or \"on-failure\")", p.ID, t.ID, t.Restart)
		}
		if t.Ready != nil {
			if !validReadyType[t.Ready.Type] {
				return fmt.Errorf("config: project %q: task %q: invalid ready.type %q (want \"port\" or \"stdout\")", p.ID, t.ID, t.Ready.Type)
			}
			if t.Ready.Type == "stdout" && t.Ready.Match == "" {
				return fmt.Errorf("config: project %q: task %q: ready.type \"stdout\" requires a non-empty match", p.ID, t.ID)
			}
		}
	}
	if primaries > 1 {
		return fmt.Errorf("config: project %q: more than one task marked primary", p.ID)
	}
	return nil
}
