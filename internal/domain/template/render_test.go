package template

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

func given(params map[string]string, secrets map[string]string) Given {
	return Given{Params: params, Secrets: secrets}
}

// ct is a well-formed ciphertext (new bytes on every call).
func ct(t testing.TB) string { return cryptotest.Ciphertext(t, "secret") }

func TestResolveCreate(t *testing.T) {
	key := ct(t)
	v, err := Resolve(tenant(), Stored{}, given(
		map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"},
		map[string]string{"SESSION_KEY": key}))
	if err != nil {
		t.Fatal(err)
	}
	wantPlain := map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001", "MEMORY_MAX": "256m", "INGEST_DISABLED": ""}
	if !reflect.DeepEqual(v.Plain, wantPlain) || !reflect.DeepEqual(v.Secrets, map[string]string{"SESSION_KEY": key}) {
		t.Fatalf("values = %+v", v)
	}
	// Defaults are not recorded: an upgrade follows the template's default.
	if !reflect.DeepEqual(v.Stored, Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, SecretNames: []string{"SESSION_KEY"}}) {
		t.Fatalf("stored = %+v", v.Stored)
	}
}

func TestResolveRefusals(t *testing.T) {
	base := map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	key := map[string]string{"SESSION_KEY": ct(t)}
	b64 := base64.StdEncoding.EncodeToString
	cases := []struct {
		name  string
		g     Given
		field string
	}{
		{"unknown", given(with("NOPE", "1"), key), "parameters.NOPE"},
		{"secret as plain", given(with("SESSION_KEY", "x"), key), "parameters.SESSION_KEY"},
		{"plain as secret", given(base, map[string]string{"SESSION_KEY": ct(t), "NAME": ct(t)}), "secrets.NAME"},
		{"unknown secret", given(base, map[string]string{"SESSION_KEY": ct(t), "NOPE": ct(t)}), "secrets.NOPE"},
		{"placeholder secret", given(base, map[string]string{"SESSION_KEY": command.EncryptedPlaceholder}), "secrets.SESSION_KEY"},
		{"plaintext secret", given(base, map[string]string{"SESSION_KEY": "hunter2"}), "secrets.SESSION_KEY"},
		{"base64 plaintext secret", given(base, map[string]string{"SESSION_KEY": b64([]byte(strings.Repeat("x", 120)))}), "secrets.SESSION_KEY"},
		{"short secret", given(base, map[string]string{"SESSION_KEY": b64(append([]byte{0x04}, make([]byte, 64+12+15)...))}), "secrets.SESSION_KEY"},
		{"dollar in a value", given(with("VERSION", "v$1"), key), "parameters.VERSION"},
		{"missing required", given(map[string]string{"NAME": "acme", "PORT": "1"}, key), "parameters.VERSION"},
		{"empty required", given(with("VERSION", ""), key), "parameters.VERSION"},
		{"missing secret", given(base, nil), "secrets.SESSION_KEY"},
		{"off pattern", given(with("NAME", "AC"), key), "parameters.NAME"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tenant(), Stored{}, tc.g)
			wantInvalid(t, err, tc.field)
		})
	}
}

func TestResolveUpdateKeepsStoredValuesAndSecrets(t *testing.T) {
	stored := Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001", "GONE": "x"}, SecretNames: []string{"SESSION_KEY", "OLD"}}
	v, err := Resolve(tenant(), stored, given(map[string]string{"VERSION": "v2"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if v.Plain["VERSION"] != "v2" || v.Plain["NAME"] != "acme" || v.Secrets["SESSION_KEY"] != command.EncryptedPlaceholder {
		t.Fatalf("values = %+v", v)
	}
	if _, ok := v.Stored.Params["GONE"]; ok || !reflect.DeepEqual(v.Stored.SecretNames, []string{"SESSION_KEY"}) {
		t.Fatalf("stored = %+v", v.Stored)
	}
	// A newly required parameter without a default must be given.
	b := tenant()
	b.Parameters = append(b.Parameters, Parameter{Name: "REGION", Required: true})
	_, err = Resolve(b, stored, Given{})
	wantInvalid(t, err, "parameters.REGION")
}

func TestRender(t *testing.T) {
	b := tenant()
	key := ct(t)
	v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, map[string]string{"SESSION_KEY": key}))
	if err != nil {
		t.Fatal(err)
	}
	backups, err := CreateBackups(b, map[string]string{"db": "fast"})
	if err != nil {
		t.Fatal(err)
	}
	marker := &Marker{TemplateID: "tpl", TemplateName: "twillingate", Revision: 3, Key: "k-1"}
	app, payload, err := Render(b, Input{AppID: "app-1", Values: v, Marker: marker, Backups: backups})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "app-1" || app.Name != "acme" || app.Icon != "chart" || app.TemplateID != "tpl" {
		t.Fatalf("app = %+v", app)
	}
	if payload.AppID != "app-1" || len(payload.Files) != 1 || string(payload.Files[0].Content) != tenantCompose {
		t.Fatalf("files = %+v", payload.Files)
	}
	vars := map[string]command.ContentItem{}
	for _, it := range payload.Variables {
		vars[it.Name] = it
	}
	if len(vars) != 6 || string(vars["NAME"].Content) != "acme" || vars["NAME"].Encrypted ||
		string(vars["SESSION_KEY"].Content) != key || !vars["SESSION_KEY"].Encrypted || string(vars["MEMORY_MAX"].Content) != "256m" {
		t.Fatalf("variables = %+v", payload.Variables)
	}
	if _, ok := vars["API_TOKEN"]; ok {
		t.Fatal("an optional secret that was never given became a variable")
	}
	if err := (command.SaveAppRequest{App: payload}).Validate(); err != nil {
		t.Fatalf("the payload fails save-app's own check: %v", err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(payload.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	ing, err := model.ParseIngress(payload.Config)
	if err != nil || ing == nil || len(ing.Domains) != 1 || ing.Domains[0].Domain != "acme.twillingate.app" || ing.Domains[0].UpstreamPort != 9001 {
		t.Fatalf("ingress = %+v, %v", ing, err)
	}
	if string(cfg["template_id"]) != `"tpl"` || string(cfg["instance"]) != `{"template_id":"tpl","template_name":"twillingate","revision":3,"key":"k-1"}` {
		t.Fatalf("marker = %s %s", cfg["template_id"], cfg["instance"])
	}
	if string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"fast"}]` {
		t.Fatalf("backups = %s", cfg["backups"])
	}
	// The reconcile reads apps.template_id from the blob.
	var listed model.App
	if err := json.Unmarshal(payload.Config, &listed); err != nil || listed.TemplateID != "tpl" || listed.Name != "acme" {
		t.Fatalf("as listed = %+v, %v", listed, err)
	}
}

func TestRenderUpdateAndUnlink(t *testing.T) {
	b := tenant()
	v, err := Resolve(b, Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, SecretNames: []string{"SESSION_KEY"}}, Given{})
	if err != nil {
		t.Fatal(err)
	}
	app, payload, err := Render(b, Input{AppID: "app-1", Values: v})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	_ = json.Unmarshal(payload.Config, &cfg)
	for _, k := range []string{"backups", "instance", "template_id"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("unlink blob carries %s", k)
		}
	}
	if app.TemplateID != "" {
		t.Fatalf("unlinked app keeps template id %q", app.TemplateID)
	}
	for _, it := range payload.Variables {
		if it.Name == "SESSION_KEY" && (string(it.Content) != command.EncryptedPlaceholder || !it.Encrypted) {
			t.Fatalf("kept secret = %+v", it)
		}
	}
}

func TestRenderRefusesANonPortValue(t *testing.T) {
	b := tenant()
	b.Parameters[2].Pattern = ""
	v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "http"}, map[string]string{"SESSION_KEY": ct(t)}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Render(b, Input{AppID: "a", Values: v})
	wantInvalid(t, err, "parameters.PORT")
}

func TestCreateBackups(t *testing.T) {
	b := tenant()
	got, err := CreateBackups(b, nil)
	if err != nil || !reflect.DeepEqual(got, []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "tenants"}}) {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	_, err = CreateBackups(b, map[string]string{"media": "x"})
	wantInvalid(t, err, "backups.media")
	_, err = CreateBackups(b, map[string]string{"db": "Bad Name"})
	wantInvalid(t, err, "backups.db.storage")
}

func TestNewBackupDefaultsOnlyForBackupsNewInTheRevision(t *testing.T) {
	prev := tenant()
	next := tenant()
	next.Files["compose.yaml"] = strings.Replace(tenantCompose,
		"    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }\n",
		"    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }\n    - { name: media, type: files, volume: data }\n", 1)
	next.Backups["db"] = BackupDefault{Enabled: true, Storage: "elsewhere"}
	next.Backups["media"] = BackupDefault{Enabled: true, Storage: "tenants", Every: "6h"}
	if err := Validate(next); err != nil {
		t.Fatal(err)
	}
	got, err := NewBackupDefaults(prev, next)
	want := []recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "tenants", Every: "6h"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("new defaults = %+v, %v", got, err)
	}
	if got, _ := NewBackupDefaults(next, next); len(got) != 0 {
		t.Fatalf("same revision = %+v", got)
	}
}

func TestRequestHashComparesSecretNamesNotBytes(t *testing.T) {
	in := HashInput{ServerID: "s", TemplateID: "t", Params: map[string]string{"A": "1", "B": "2"}, SecretNames: []string{"K2", "K1"}}
	h := RequestHash(in)
	same := in
	same.Params = map[string]string{"B": "2", "A": "1"}
	same.SecretNames = []string{"K1", "K2"}
	if RequestHash(same) != h {
		t.Fatal("order changed the hash")
	}
	for name, edit := range map[string]func(*HashInput){
		"server":   func(i *HashInput) { i.ServerID = "s2" },
		"revision": func(i *HashInput) { i.Revision = 2 },
		"value":    func(i *HashInput) { i.Params = map[string]string{"A": "1", "B": "3"} },
		"secrets":  func(i *HashInput) { i.SecretNames = []string{"K1"} },
		"backups":  func(i *HashInput) { i.Backups = map[string]string{"db": "x"} },
	} {
		c := in
		edit(&c)
		if RequestHash(c) == h {
			t.Errorf("%s did not change the hash", name)
		}
	}
	if len(h) != 64 {
		t.Fatalf("hash = %q", h)
	}
}
