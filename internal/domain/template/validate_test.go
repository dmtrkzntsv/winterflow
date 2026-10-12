package template

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

const tenantCompose = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:${VERSION}
    mem_limit: ${MEMORY_MAX}
    ports: ["127.0.0.1:${PORT}:8080"]
    environment:
      PUBLIC_URL: https://${NAME}.twillingate.app
      SMTP_PASS: ${SMTP_PASS}
      OPTIONAL: ${OPTIONAL:-x}
      SESSION_KEY: ${SESSION_KEY}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }
`

func strp(s string) *string { return &s }

// tenant is the spec's twillingate template, with a host port parameter.
func tenant() Body {
	return Body{
		Files: map[string]string{"compose.yaml": tenantCompose},
		Parameters: []Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]{3,20}$", Description: "Instance name"},
			{Name: "VERSION", Required: true},
			{Name: "PORT", Required: true, Pattern: "^[0-9]+$"},
			{Name: "MEMORY_MAX", Default: strp("256m")},
			{Name: "INGEST_DISABLED", Default: strp("")},
			{Name: "SESSION_KEY", Secret: true, Required: true},
			{Name: "API_TOKEN", Secret: true},
		},
		AppName: "${NAME}",
		Icon:    "chart",
		Color:   "#2563eb",
		Ingress: []IngressRow{{Host: "${NAME}.twillingate.app", Service: "app", Port: "${PORT}"}},
		Backups: map[string]BackupDefault{"db": {Enabled: true, Storage: "tenants"}},
	}
}

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, model.ErrRejected) {
		t.Fatalf("want an invalid-template error naming %s, got %v", field, err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error %q does not name %s", err, field)
	}
}

func TestValidateAcceptsTheTenantTemplate(t *testing.T) {
	if err := Validate(tenant()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(b *Body)
		field string
	}{
		{"no compose file", func(b *Body) { b.Files = map[string]string{"app.env": "x"} }, "files"},
		{"absolute file", func(b *Body) { b.Files["/etc/passwd"] = "x" }, "files"},
		{"escaping file", func(b *Body) { b.Files["../x"] = "x" }, "files"},
		{"winterflow file", func(b *Body) { b.Files[".winterflow/config.json"] = "{}" }, "files"},
		{"agent env file", func(b *Body) { b.Files[".env"] = "A=1" }, "files"},
		{"lowercase parameter", func(b *Body) { b.Parameters[0].Name = "name" }, "parameters[0].name"},
		{"reserved WF_", func(b *Body) { b.Parameters[0].Name = "WF_APP_ID" }, "parameters[0].name"},
		{"reserved project", func(b *Body) { b.Parameters[0].Name = "COMPOSE_PROJECT_NAME" }, "parameters[0].name"},
		{"duplicate", func(b *Body) { b.Parameters[1].Name = "NAME" }, "parameters[1].name"},
		{"bad pattern", func(b *Body) { b.Parameters[0].Pattern = "([a-z" }, "parameters[0].pattern"},
		{"long pattern", func(b *Body) { b.Parameters[0].Pattern = strings.Repeat("a", 513) }, "parameters[0].pattern"},
		{"default off pattern", func(b *Body) { b.Parameters[0].Default = strp("UPPER") }, "parameters[0].default"},
		{"default with $", func(b *Body) { b.Parameters[3].Default = strp("$HOME") }, "parameters[3].default"},
		{"secret default", func(b *Body) { b.Parameters[5].Default = strp("x") }, "parameters[5].default"},
		{"empty app name", func(b *Body) { b.AppName = " " }, "app_name"},
		{"app name uses a secret", func(b *Body) { b.AppName = "${SESSION_KEY}" }, "app_name"},
		{"app name uses an unknown", func(b *Body) { b.AppName = "${NOPE}" }, "app_name"},
		{"host uses a secret", func(b *Body) { b.Ingress[0].Host = "${SESSION_KEY}.x.app" }, "ingress[0].host"},
		{"host syntax", func(b *Body) { b.Ingress[0].Host = "Bad_Host" }, "ingress"},
		{"port range", func(b *Body) { b.Ingress[0].Port = "70000" }, "ingress[0].port"},
		{"port mixed", func(b *Body) { b.Ingress[0].Port = "80${PORT}" }, "ingress[0].port"},
		{"port secret", func(b *Body) { b.Ingress[0].Port = "${SESSION_KEY}" }, "ingress[0].port"},
		{"backup not in recipe", func(b *Body) { b.Backups["media"] = BackupDefault{} }, "backups.media"},
		{"enabled backup without storage", func(b *Body) { b.Backups["db"] = BackupDefault{Enabled: true} }, "backups.db.storage"},
		{"backup every", func(b *Body) { b.Backups["db"] = BackupDefault{Every: "1s"} }, "backups.db.every"},
		{"broken recipe", func(b *Body) {
			b.Files["compose.yaml"] = "services: {app: {image: x}}\nx-winterflow:\n  backups:\n    - {name: db, type: sqlite, volume: nope, path: a.db}\n"
		}, "files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tenant()
			tc.edit(&b)
			wantInvalid(t, Validate(b), tc.field)
		})
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"twillingate", "a", "shop-2"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Shop", "2shop", "-x", strings.Repeat("a", 64), "a_b"} {
		wantInvalid(t, ValidateName(bad), "name")
	}
}

func TestParseCapsTheBody(t *testing.T) {
	raw := []byte(`{"files":{"compose.yaml":"` + strings.Repeat("x", MaxBodyBytes) + `"}}`)
	wantInvalid(t, func() error { _, err := Parse(raw); return err }(), "body")
	_, err := Parse([]byte(`{"files": 3}`))
	wantInvalid(t, err, "body")
}

func TestPortJSON(t *testing.T) {
	var rows []IngressRow
	if err := json.Unmarshal([]byte(`[{"host":"a.test","port":8080},{"host":"b.test","port":"${PORT}"}]`), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Port != "8080" || rows[1].Port != "${PORT}" {
		t.Fatalf("ports = %q %q", rows[0].Port, rows[1].Port)
	}
	out, _ := json.Marshal(rows)
	if want := `[{"host":"a.test","port":8080},{"host":"b.test","port":"${PORT}"}]`; string(out) != want {
		t.Fatalf("marshal = %s", out)
	}
	if err := json.Unmarshal([]byte(`[{"host":"a","port":true}]`), &rows); err == nil {
		t.Fatal("a boolean port was accepted")
	}
	if raw, err := Port("").JSONSchemaBytes(); err != nil || !json.Valid(raw) {
		t.Fatalf("schema = %s, %v", raw, err)
	}
}

func TestWarningsNameUnprovidedReferences(t *testing.T) {
	shared := func(name string) bool { return name == "SMTP_PASS" }
	if got := Warnings(tenant(), shared); len(got) != 0 {
		t.Fatalf("warnings = %v", got)
	}
	got := Warnings(tenant(), func(string) bool { return false })
	want := []string{"SMTP_PASS is not a parameter; it must come from a shared variable on the server"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %v", got)
	}
}
