package template

import (
	"strings"
	"testing"

	"winterflow/internal/domain/compose/xwinterflow"
)

func TestValidateAcceptsTheExample(t *testing.T) {
	s := spec(t)
	if len(s.Variables) != 4 || len(s.Expose) != 2 || len(s.Compose.Recipe) != 1 {
		t.Fatalf("spec = %+v", s)
	}
	if v, _ := s.Variable("LANGUAGE"); v.Default == nil || *v.Default != "en" {
		t.Fatalf("LANGUAGE = %+v", v)
	}
	if v, _ := s.Variable("SESSION_SECRET"); v.Default != nil {
		t.Fatalf("a secret has a default: %+v", v)
	}
}

func TestValidateRules(t *testing.T) {
	cases := map[string]struct {
		edit  func(*Body)
		field string
	}{
		"no compose file":      {func(b *Body) { b.Files = map[string]string{"config.yml": "x"} }, "a compose file is required"},
		"a reserved file":      {func(b *Body) { b.Files[".env"] = "A=1" }, `".env" is not a usable file path`},
		"a path outside":       {func(b *Body) { b.Files["../x"] = "" }, `"../x" is not a usable file path`},
		"under .winterflow":    {func(b *Body) { b.Files[".winterflow/config.json"] = "{}" }, "is not a usable file path"},
		"a two-line summary":   {func(b *Body) { b.Summary = "a\nb" }, "summary"},
		"a long summary":       {func(b *Body) { b.Summary = strings.Repeat("s", 201) }, "summary"},
		"a long description":   {func(b *Body) { b.Description = strings.Repeat("d", 4001) }, "description"},
		"a padded icon":        {func(b *Body) { b.Icon = " note" }, "icon"},
		"a long color":         {func(b *Body) { b.Color = strings.Repeat("c", 33) }, "color"},
		"not YAML":             {func(b *Body) { b.Files["compose.yaml"] = "services: [\n" }, "not valid YAML"},
		"an unknown block key": {func(b *Body) { b.Files["compose.yaml"] += "  volumes: {}\n" }, "x-winterflow.volumes"},
		"a default off pattern": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, `LANGUAGE: "en"`, `LANGUAGE: "english"`, 1)
		}, "x-winterflow.variables.LANGUAGE.pattern"},
		"a literal volume name": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, "  pg:\nx-", "  pg: { name: shared-pg }\nx-", 1)
		}, "would be shared by every install"},
		"a bad recipe": {func(b *Body) {
			b.Files["compose.yaml"] = strings.Replace(notesCompose, "volume: pg", "volume: nope", 1)
		}, "nope"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := notes()
			c.edit(&b)
			_, err := Validate(b)
			wantInvalid(t, err, c.field)
		})
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("notes-2"); err != nil {
		t.Fatal(err)
	}
	wantInvalid(t, ValidateName("Notes"), "name")
}

func TestEncodeCapsTheBody(t *testing.T) {
	b := notes()
	b.Files["big.txt"] = strings.Repeat("x", MaxBodyBytes)
	_, err := Encode(b)
	wantInvalid(t, err, "body")
}

// Warnings name the parser's (a variable nothing uses) and each reference
// nothing provides; declared, reserved, shared and defaulted ones are not.
func TestWarnings(t *testing.T) {
	b := notes()
	b.Files["compose.yaml"] = strings.Replace(notesCompose, "    DB_PASSWORD: { encrypted: true }\n", "    DB_PASSWORD: { encrypted: true }\n    UNUSED: {}\n", 1) +
		"# ${WF_APP_ID} ${ORG_SMTP}\n"
	s, err := Validate(b)
	if err != nil {
		t.Fatal(err)
	}
	got := Warnings(b, s, func(name string) bool { return name == "ORG_SMTP" })
	if len(got) != 2 || !strings.Contains(got[0], "UNUSED") || !strings.HasPrefix(got[1], "SMTP_HOST is not a declared variable") {
		t.Fatalf("warnings = %q", got)
	}
	if got := Warnings(b, s, nil); len(got) != 3 {
		t.Fatalf("without shared names = %q", got)
	}
}

func TestDetect(t *testing.T) {
	d := Detect(spec(t), []string{"w"})
	if len(d.Variables) != 4 || d.Variables[0].Name != "BASE_URL" || !d.Variables[0].Required {
		t.Fatalf("variables = %+v", d.Variables)
	}
	if len(d.Expose) != 2 || d.Expose[0].ID != "app:8080/http" || d.Expose[0].Target != "app:8080" || d.Expose[1].Protocol != "tcp" {
		t.Fatalf("expose = %+v", d.Expose)
	}
	if len(d.Backups) != 1 || d.Backups[0].Name != "db" || d.Backups[0].Type != "files" || len(d.Warnings) != 1 {
		t.Fatalf("detected = %+v", d)
	}
	empty := Detect(xwinterflow.Spec{}, nil)
	if empty.Variables == nil || empty.Expose == nil || empty.Backups == nil || empty.Warnings == nil {
		t.Fatalf("an empty detection has nil lists: %+v", empty)
	}
}
