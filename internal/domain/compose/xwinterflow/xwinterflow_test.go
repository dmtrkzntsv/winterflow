package xwinterflow

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

const notes = `
services:
  app:
    image: ghcr.io/example/notes:latest
    environment:
      # Public URL the browser uses, e.g. https://notes.example.com
      BASE_URL: "http://localhost:8080"
      # Secret used to sign sessions.
      SESSION_SECRET: "changeme"
      LANGUAGE: "en"
    volumes:
      - data:/var/lib/notes
  db:
    image: postgres:16
    environment:
      - POSTGRES_USER=notes
      # the database password
      - POSTGRES_PASSWORD=notes
      - LANGUAGE
    volumes:
      - pg:/var/lib/postgresql/data
volumes:
  data:
  pg:
x-winterflow:
  expose:
    - container: app
      port: 8080
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL:       { required: true, pattern: "^https?://.+" }
    SESSION_SECRET: { required: true, encrypted: true }
    LANGUAGE:       { pattern: "^[a-z]{2}$", description: "Two-letter UI language" }
    POSTGRES_PASSWORD: { encrypted: true }
    DB_PASSWORD:    { encrypted: true }
  overrides:
    services:
      app:
        environment:
          DATABASE_URL: postgres://notes:${DB_PASSWORD}@db:5432/notes
  files:
    - config.yml
  backups:
    - name: db
      type: files
      volume: pg
      every: 24h
`

func parse(t *testing.T, mode Mode, files ...string) Spec {
	t.Helper()
	raw := make([][]byte, len(files))
	for i, f := range files {
		raw[i] = []byte(f)
	}
	s, err := Parse(mode, raw...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func str(s string) *string { return &s }

func TestParse(t *testing.T) {
	s := parse(t, ModeApp, notes)
	if !s.Declared {
		t.Fatal("block not seen")
	}
	wantExpose := []Expose{{"app", 8080, ProtocolHTTP}, {"db", 5432, ProtocolTCP}}
	if !reflect.DeepEqual(s.Expose, wantExpose) {
		t.Fatalf("expose = %+v", s.Expose)
	}
	if s.Expose[0].ID() != "app:8080/http" || s.Expose[0].Target() != "app:8080" || s.Expose[0].Bind() != "127.0.0.1" || s.Expose[1].Bind() != "0.0.0.0" {
		t.Fatalf("ids = %s %s %s", s.Expose[0].ID(), s.Expose[0].Target(), s.Expose[1].Bind())
	}
	want := []DeclaredVariable{
		{Name: "BASE_URL", Required: true, Pattern: "^https?://.+", Description: "Public URL the browser uses, e.g. https://notes.example.com",
			Default: str("http://localhost:8080"), Services: []string{"app"}},
		{Name: "SESSION_SECRET", Required: true, Encrypted: true, Description: "Secret used to sign sessions.", Services: []string{"app"}},
		{Name: "LANGUAGE", Pattern: "^[a-z]{2}$", Description: "Two-letter UI language", Default: str("en"), Services: []string{"app", "db"}},
		{Name: "POSTGRES_PASSWORD", Encrypted: true, Description: "the database password", Services: []string{"db"}},
		{Name: "DB_PASSWORD", Encrypted: true, Services: []string{}},
	}
	if !reflect.DeepEqual(s.Variables, want) {
		t.Fatalf("variables =\n%+v\nwant\n%+v", s.Variables, want)
	}
	if s.Overrides["app"] == nil || len(s.Overrides) != 1 {
		t.Fatalf("overrides = %v", s.Overrides)
	}
	if !reflect.DeepEqual(s.Files, []string{"config.yml"}) {
		t.Fatalf("files = %v", s.Files)
	}
	if len(s.Compose.Recipe) != 1 || s.Compose.Recipe[0].Name != "db" || !s.Compose.Volumes["pg"] {
		t.Fatalf("compose = %+v", s.Compose)
	}
	if len(s.Warnings) != 0 {
		t.Fatalf("warnings = %v (DB_PASSWORD is used by an override)", s.Warnings)
	}
	if v, ok := s.Variable("LANGUAGE"); !ok || v.Pattern != "^[a-z]{2}$" {
		t.Fatalf("Variable = %+v %v", v, ok)
	}
	if _, ok := s.Variable("NOPE"); ok {
		t.Fatal("unknown variable found")
	}
	if got := s.HTTPTargets(); !reflect.DeepEqual(got, map[string]bool{"app:8080": true}) {
		t.Fatalf("http targets = %v", got)
	}
}

func TestParseWithoutBlock(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: nginx\n")
	if s.Declared || len(s.Expose)+len(s.Variables)+len(s.Overrides)+len(s.Files) != 0 {
		t.Fatalf("spec = %+v", s)
	}
	s = parse(t, ModeApp, "services: [\n")
	if !s.Compose.Unparsed || s.Declared {
		t.Fatalf("an unparsable file: %+v", s)
	}
	if s = parse(t, ModeApp, ""); s.Declared {
		t.Fatalf("an empty file: %+v", s)
	}
}

// A later file's variable and override keys replace the earlier file's; an
// expose entry repeated in the override file stays once.
func TestParseMergesFiles(t *testing.T) {
	main := "services:\n  app:\n    image: a\n    environment: { A: one }\nx-winterflow:\n  expose: [{ container: app, port: 80 }]\n  variables:\n    A: { required: true }\n  overrides:\n    services:\n      app: { command: [one], user: root }\n"
	over := "services:\n  app:\n    environment: [B=two]\nx-winterflow:\n  expose: [{ container: app, port: 80 }, { container: app, port: 53, protocol: udp }]\n  variables:\n    A: { pattern: 'o.e' }\n    B: {}\n  overrides:\n    services:\n      app: { command: [two] }\n"
	s := parse(t, ModeApp, main, over)
	if len(s.Expose) != 2 || s.Expose[1].ID() != "app:53/udp" {
		t.Fatalf("expose = %+v", s.Expose)
	}
	if len(s.Variables) != 2 || s.Variables[0].Required || s.Variables[0].Pattern != "o.e" || *s.Variables[1].Default != "two" {
		t.Fatalf("variables = %+v", s.Variables)
	}
	app := s.Overrides["app"]
	if got := value(app, "command").Content[0].Value; got != "two" || value(app, "user").Value != "root" {
		t.Fatalf("override = %v / %v", got, value(app, "user"))
	}
}

func TestParseRules(t *testing.T) {
	const body = "services:\n  app:\n    image: a\n    environment:\n      LANG: en\n"
	cases := []struct {
		name, x, field string
	}{
		{"not a mapping", "x-winterflow: [1]\n", "x-winterflow: must be a mapping"},
		{"unknown key", "x-winterflow:\n  volumes: {}\n", "x-winterflow.volumes: unknown key"},
		{"expose not a list", "x-winterflow:\n  expose: {}\n", "x-winterflow.expose: must be a list"},
		{"expose unknown key", "x-winterflow:\n  expose: [{ container: app, port: 80, host: 1 }]\n", "x-winterflow.expose[0].host: unknown key"},
		{"expose unknown service", "x-winterflow:\n  expose: [{ container: web, port: 80 }]\n", `x-winterflow.expose[0].container: "web" is not a service`},
		{"expose port range", "x-winterflow:\n  expose: [{ container: app, port: 70000 }]\n", "x-winterflow.expose[0].port: 70000 is not a port"},
		{"expose port type", "x-winterflow:\n  expose: [{ container: app, port: http }]\n", "cannot unmarshal !!str `http` into int"},
		{"expose protocol", "x-winterflow:\n  expose: [{ container: app, port: 80, protocol: sctp }]\n", `x-winterflow.expose[0].protocol: "sctp" is not http, tcp or udp`},
		{"expose duplicate", "x-winterflow:\n  expose: [{ container: app, port: 80 }, { container: app, port: 80, protocol: http }]\n", "x-winterflow.expose[1]: app:80/http is listed twice"},
		{"variables not a mapping", "x-winterflow:\n  variables: [A]\n", "x-winterflow.variables: must be a mapping"},
		{"variable name", "x-winterflow:\n  variables:\n    lang: {}\n", "x-winterflow.variables.lang: a name is upper-case"},
		{"variable reserved WF_", "x-winterflow:\n  variables:\n    WF_APP_ID: {}\n", "x-winterflow.variables.WF_APP_ID: WF_APP_ID is reserved"},
		{"variable reserved project", "x-winterflow:\n  variables:\n    COMPOSE_PROJECT_NAME: {}\n", "COMPOSE_PROJECT_NAME is reserved"},
		{"variable unknown key", "x-winterflow:\n  variables:\n    LANG: { default: en }\n", "x-winterflow.variables.LANG.default: unknown key"},
		{"variable bad type", "x-winterflow:\n  variables:\n    LANG: { required: maybe }\n", "cannot unmarshal !!str `maybe` into bool"},
		{"pattern too long", "x-winterflow:\n  variables:\n    LANG: { pattern: '" + strings.Repeat("a", 513) + "' }\n", "x-winterflow.variables.LANG.pattern: at most 512 bytes"},
		{"pattern not RE2", "x-winterflow:\n  variables:\n    LANG: { pattern: '(?=a)' }\n", "x-winterflow.variables.LANG.pattern: error parsing regexp"},
		{"description too long", "x-winterflow:\n  variables:\n    LANG: { description: '" + strings.Repeat("d", 1001) + "' }\n", "x-winterflow.variables.LANG.description: at most 1000 bytes"},
		{"default fails pattern", "x-winterflow:\n  variables:\n    LANG: { pattern: '[a-z]{3}' }\n", `x-winterflow.variables.LANG.pattern: the compose file's own value "en" does not match [a-z]{3}`},
		{"overrides other than services", "x-winterflow:\n  overrides:\n    volumes: {}\n", "x-winterflow.overrides.volumes: only services can be overridden"},
		{"overrides unknown service", "x-winterflow:\n  overrides:\n    services:\n      web: { user: root }\n", `x-winterflow.overrides.services.web: "web" is not a service`},
		{"override not a mapping", "x-winterflow:\n  overrides:\n    services:\n      app: [1]\n", "x-winterflow.overrides.services.app: must be a mapping"},
		{"files not a list", "x-winterflow:\n  files: config.yml\n", "x-winterflow.files: must be a list"},
		{"file outside", "x-winterflow:\n  files: [../etc/passwd]\n", `x-winterflow.files[0]: "../etc/passwd" is not a path`},
		{"file absolute", "x-winterflow:\n  files: [/etc/passwd]\n", `x-winterflow.files[0]: "/etc/passwd" is not a path`},
		{"backups still checked", "x-winterflow:\n  backups: [{ name: Bad, type: files }]\n", "x-winterflow.backups[0].name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(ModeApp, []byte(body+tc.x))
			if err == nil || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("err = %v, want %q", err, tc.field)
			}
		})
	}
}

func TestParseVolumeNameRule(t *testing.T) {
	const named = "services:\n  app:\n    image: a\nvolumes:\n  data:\n    name: notes-data\n  shared:\n    name: media\n    external: true\n  scoped:\n    name: notes-${WF_APP_ID}\n  plain: {}\n"
	s := parse(t, ModeApp, named)
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], `volumes.data: the name "notes-data" would be shared`) {
		t.Fatalf("app warnings = %v", s.Warnings)
	}
	if _, err := Parse(ModeTemplate, []byte(named)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "volumes.data") {
		t.Fatalf("template err = %v", err)
	}
}

func TestParseUnusedVariableWarns(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\nx-winterflow:\n  variables:\n    LONELY: {}\n")
	if len(s.Warnings) != 1 || s.Warnings[0] != "x-winterflow.variables.LONELY: no service environment and no override uses it" {
		t.Fatalf("warnings = %v", s.Warnings)
	}
	if s.Variables[0].Default != nil || len(s.Variables[0].Services) != 0 {
		t.Fatalf("variable = %+v", s.Variables[0])
	}
}

func TestDescriptionFromComment(t *testing.T) {
	long := strings.Repeat("é", 600) // 1200 bytes
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\n    environment:\n      # "+long+"\n      NOTE: x\nx-winterflow:\n  variables:\n    NOTE: {}\n")
	d := s.Variables[0].Description
	if len(d) > MaxDescription || !strings.HasPrefix(long, d) || d == "" {
		t.Fatalf("description cut to %d bytes", len(d))
	}
}
