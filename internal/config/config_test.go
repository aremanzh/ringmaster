package config

import (
	"strings"
	"testing"
)

// validYAML is a minimal well-formed config reused as a base by tests.
const validYAML = `
version: 1
php_roots:
  - "C:\\laragon\\bin\\php"
defaults:
  host: "127.0.0.1"
  port_range: [8000, 8099]
projects:
  - id: "shop"
    label: "Shop"
    path: "C:\\laragon\\www\\shop"
    php: "8.2"
    port: 8000
    tasks:
      - id: "serve"
        argv: ["{php}", "artisan", "serve", "--host={host}", "--port={port}"]
        primary: true
        ready: { type: "port" }
      - id: "vite"
        argv: ["npm.cmd", "run", "dev", "--", "--host={host}"]
        ready: { type: "stdout", match: "ready in" }
      - id: "queue"
        argv: ["{php}", "artisan", "queue:work"]
        autostart: false
        restart: "on-failure"
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Projects) != 1 || len(cfg.Projects[0].Tasks) != 3 {
		t.Fatalf("unexpected shape: %+v", cfg)
	}
	p := cfg.Projects[0]
	if p.ID != "shop" || p.Port != 8000 || p.PHP != "8.2" {
		t.Errorf("project fields wrong: %+v", p)
	}
	if !p.Tasks[0].Primary || p.Tasks[0].Ready.Type != "port" {
		t.Errorf("serve task wrong: %+v", p.Tasks[0])
	}
	if p.Tasks[0].AutostartEnabled() != true {
		t.Error("serve: autostart should default to true")
	}
	if p.Tasks[2].AutostartEnabled() != false {
		t.Error("queue: autostart false not honored")
	}
	if p.Tasks[2].Restart != "on-failure" {
		t.Errorf("queue restart = %q", p.Tasks[2].Restart)
	}
	if _, err := Parse([]byte(strings.Replace(validYAML, `restart: "on-failure"`, `restart: "always"`, 1))); err != nil {
		t.Errorf(`restart: "always" rejected: %v`, err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr string
	}{
		{
			name:    "unsupported version",
			mutate:  func(s string) string { return strings.Replace(s, "version: 1", "version: 2", 1) },
			wantErr: "unsupported version",
		},
		{
			name:    "missing version",
			mutate:  func(s string) string { return strings.Replace(s, "version: 1", "", 1) },
			wantErr: `missing "version: 1"`,
		},
		{
			name: "cleanup escapes the project",
			mutate: func(s string) string {
				return s + `
  - id: "esc"
    path: "C:\\x"
    tasks:
      - id: "vite"
        argv: ["cmd"]
        cleanup: ["..\\other\\public\\hot"]
`
			},
			wantErr: "cleanup path",
		},
		{
			name: "cleanup is absolute",
			mutate: func(s string) string {
				return s + `
  - id: "abs"
    path: "C:\\x"
    tasks:
      - id: "vite"
        argv: ["cmd"]
        cleanup: ["C:\\Windows\\win.ini"]
`
			},
			wantErr: "cleanup path",
		},
		{
			name: "duplicate project id",
			mutate: func(s string) string {
				return s + `
  - id: "shop"
    path: "C:\\x"
    tasks:
      - id: "serve"
        argv: ["cmd"]
`
			},
			wantErr: "duplicate project id",
		},
		{
			name: "duplicate port",
			mutate: func(s string) string {
				return s + `
  - id: "other"
    path: "C:\\x"
    port: 8000
    tasks:
      - id: "serve"
        argv: ["cmd"]
`
			},
			wantErr: "both declare port 8000",
		},
		{
			name:    "duplicate task id",
			mutate:  func(s string) string { return strings.Replace(s, `id: "vite"`, `id: "serve"`, 1) },
			wantErr: "duplicate task id",
		},
		{
			name: "empty argv",
			mutate: func(s string) string {
				return strings.Replace(s, `argv: ["{php}", "artisan", "queue:work"]`, `argv: []`, 1)
			},
			wantErr: "argv must be a non-empty array",
		},
		{
			name:    "unknown placeholder",
			mutate:  func(s string) string { return strings.Replace(s, "--port={port}", "--port={prot}", 1) },
			wantErr: "unknown placeholder {prot}",
		},
		{
			name:    "invalid restart policy",
			mutate:  func(s string) string { return strings.Replace(s, `restart: "on-failure"`, `restart: "sometimes"`, 1) },
			wantErr: "invalid restart policy",
		},
		{
			name:    "stdout ready without match",
			mutate:  func(s string) string { return strings.Replace(s, `, match: "ready in"`, "", 1) },
			wantErr: `ready.type "stdout" requires a non-empty match`,
		},
		{
			name:    "invalid ready type",
			mutate:  func(s string) string { return strings.Replace(s, `type: "port"`, `type: "http"`, 1) },
			wantErr: "invalid ready.type",
		},
		{
			name:    "port out of range",
			mutate:  func(s string) string { return strings.Replace(s, "port: 8000", "port: 70000", 1) },
			wantErr: "port 70000 out of range",
		},
		{
			name:    "bad port_range",
			mutate:  func(s string) string { return strings.Replace(s, "[8000, 8099]", "[8099, 8000]", 1) },
			wantErr: "not a valid ascending port range",
		},
		{
			name: "more than one primary",
			mutate: func(s string) string {
				return strings.Replace(s, `ready: { type: "stdout", match: "ready in" }`, "primary: true", 1)
			},
			wantErr: "more than one task marked primary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.mutate(validYAML)))
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %q", tt.wantErr, err)
			}
		})
	}
}

func TestInterpolate(t *testing.T) {
	vars := Vars{PHP: `C:\php\php.exe`, Host: "127.0.0.1", Port: 8000}

	tests := []struct {
		name    string
		argv    []string
		vars    Vars
		want    []string
		wantErr string
	}{
		{
			name: "all tokens",
			argv: []string{"{php}", "artisan", "serve", "--host={host}", "--port={port}"},
			vars: vars,
			want: []string{`C:\php\php.exe`, "artisan", "serve", "--host=127.0.0.1", "--port=8000"},
		},
		{
			name: "no tokens untouched",
			argv: []string{"npm.cmd", "run", "dev"},
			vars: vars,
			want: []string{"npm.cmd", "run", "dev"},
		},
		{
			name: "multiple tokens in one element",
			argv: []string{"--url={host}:{port}"},
			vars: vars,
			want: []string{"--url=127.0.0.1:8000"},
		},
		{
			name:    "missing php value",
			argv:    []string{"{php}", "-v"},
			vars:    Vars{Host: "127.0.0.1", Port: 8000},
			wantErr: "has no resolved value",
		},
		{
			name:    "missing port value",
			argv:    []string{"--port={port}"},
			vars:    Vars{PHP: "x", Host: "h"},
			wantErr: "has no resolved value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Interpolate(tt.argv, tt.vars)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Interpolate: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("argv[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExternalTaskValidation(t *testing.T) {
	base := `
version: 1
projects:
  - id: app
    path: C:\x
    port: 8080
    tasks:
      - id: web
        external: true
`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("valid external task rejected: %v", err)
	}
	bad := `
version: 1
projects:
  - id: app
    path: C:\x
    tasks:
      - id: web
        external: true
        argv: ["x"]
`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("external task with argv must be rejected")
	}
}
