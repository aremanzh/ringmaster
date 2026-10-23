package config

import (
	"fmt"
	"regexp"
	"strings"
)

// placeholderPattern matches {token} occurrences inside argv strings.
var placeholderPattern = regexp.MustCompile(`\{([a-z_]+)\}`)

// knownPlaceholders are the tokens supported inside argv strings.
var knownPlaceholders = map[string]bool{"php": true, "host": true, "port": true}

// checkPlaceholders rejects argv strings referencing unknown tokens, so a
// typo like {prot} fails at load time instead of spawning a broken command.
func checkPlaceholders(arg string) error {
	for _, m := range placeholderPattern.FindAllStringSubmatch(arg, -1) {
		if !knownPlaceholders[m[1]] {
			return fmt.Errorf("unknown placeholder {%s} in argv element %q", m[1], arg)
		}
	}
	return nil
}

// Vars holds the resolved values substituted into argv placeholders.
type Vars struct {
	// PHP is the absolute path to the resolved php.exe.
	PHP string
	// Host is the resolved bind host.
	Host string
	// Port is the resolved port.
	Port int
}

// Interpolate returns a copy of argv with {php}, {host}, and {port} replaced
// by the values in vars. It errors if a referenced value is empty (e.g. a
// task uses {php} but no PHP binary was resolved).
func Interpolate(argv []string, vars Vars) ([]string, error) {
	port := ""
	if vars.Port != 0 {
		port = fmt.Sprintf("%d", vars.Port)
	}
	values := map[string]string{"php": vars.PHP, "host": vars.Host, "port": port}

	out := make([]string, len(argv))
	for i, arg := range argv {
		var err error
		out[i] = placeholderPattern.ReplaceAllStringFunc(arg, func(m string) string {
			token := strings.Trim(m, "{}")
			v, ok := values[token]
			if !ok {
				err = fmt.Errorf("config: unknown placeholder %s in %q", m, arg)
				return m
			}
			if v == "" {
				err = fmt.Errorf("config: placeholder %s in %q has no resolved value", m, arg)
				return m
			}
			return v
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
