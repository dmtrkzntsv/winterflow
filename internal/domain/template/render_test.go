package template

import (
	"encoding/json"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

func TestRenderIsWhatTheEditorWouldSend(t *testing.T) {
	ct := cryptotest.Ciphertext(t, "s3cret")
	app, p, err := Render(notes(), Input{
		AppID: "app-1",
		Name:  "My notes",
		Values: Values{
			Plain:   map[string]string{"BASE_URL": "https://notes.example.test"},
			Secrets: map[string]string{"SESSION_SECRET": ct, "DB_PASSWORD": command.EncryptedPlaceholder},
		},
		Hosts:   []model.InstanceHost{{Host: "notes.example.test", Expose: "app:8080"}},
		Marker:  &Marker{TemplateID: "tpl-1", TemplateName: "notes", Revision: 2, Key: "k"},
		Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "nas"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "app-1" || app.Name != "My notes" || app.Icon != "note" || app.Color != "#112233" || app.TemplateID != "tpl-1" {
		t.Fatalf("app = %+v", app)
	}
	if len(p.Files) != 1 || p.Files[0].Name != "compose.yaml" || string(p.Files[0].Content) != notesCompose {
		t.Fatalf("files = %+v", p.Files)
	}
	want := []command.ContentItem{
		{Name: "BASE_URL", Content: []byte("https://notes.example.test")},
		{Name: "DB_PASSWORD", Content: []byte(command.EncryptedPlaceholder), Encrypted: true},
		{Name: "SESSION_SECRET", Content: []byte(ct), Encrypted: true},
	}
	if len(p.Variables) != len(want) {
		t.Fatalf("variables = %+v", p.Variables)
	}
	for i, v := range want {
		if p.Variables[i].Name != v.Name || string(p.Variables[i].Content) != string(v.Content) || p.Variables[i].Encrypted != v.Encrypted {
			t.Fatalf("variable %d = %+v, want %+v", i, p.Variables[i], v)
		}
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(p.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"name":        `"My notes"`,
		"template_id": `"tpl-1"`,
		"ingress":     `{"domains":[{"domain":"notes.example.test","expose":"app:8080","ssl":false}],"redirects":[]}`,
		"instance":    `{"template_id":"tpl-1","template_name":"notes","revision":2,"key":"k"}`,
		"backups":     `[{"name":"db","enabled":true,"storage":"nas"}]`,
		"variables":   `[{"name":"BASE_URL","is_encrypted":false},{"name":"DB_PASSWORD","is_encrypted":true},{"name":"SESSION_SECRET","is_encrypted":true}]`,
	} {
		if string(cfg[key]) != want {
			t.Fatalf("config %s = %s, want %s", key, cfg[key], want)
		}
	}
	if _, ok := cfg["ports"]; ok {
		t.Fatal("a render sent ports: the agent would drop the app's pins")
	}
}

// Without a marker (unlink) or backups (an update) the config says
// nothing of them; without hosts the ingress is empty, not absent.
func TestRenderWithoutMarkerOrBackups(t *testing.T) {
	_, p, err := Render(notes(), Input{AppID: "a", Name: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	_ = json.Unmarshal(p.Config, &cfg)
	if _, ok := cfg["instance"]; ok {
		t.Fatalf("config = %s", p.Config)
	}
	if _, ok := cfg["backups"]; ok {
		t.Fatalf("config = %s", p.Config)
	}
	if _, ok := cfg["template_id"]; ok || string(cfg["ingress"]) != `{"domains":[],"redirects":[]}` {
		t.Fatalf("config = %s", p.Config)
	}
	_, _, err = Render(notes(), Input{AppID: "a", Name: ""})
	wantInvalid(t, err, "name")
}

func TestRequestHash(t *testing.T) {
	in := HashInput{ServerID: "s", TemplateID: "t", Name: "n", Values: map[string]string{"A": "1"}, SecretNames: []string{"B", "A"},
		Hosts: []model.InstanceHost{{Host: "a.test", Expose: "app:80"}}, Backups: map[string]BackupChoice{"db": {Enabled: true, Storage: "nas"}}}
	same := in
	same.SecretNames = []string{"A", "B"}
	if RequestHash(in) != RequestHash(same) {
		t.Fatal("the order of secret names changed the hash")
	}
	for name, edit := range map[string]func(*HashInput){
		"name":     func(h *HashInput) { h.Name = "m" },
		"value":    func(h *HashInput) { h.Values = map[string]string{"A": "2"} },
		"hosts":    func(h *HashInput) { h.Hosts = nil },
		"backups":  func(h *HashInput) { h.Backups = map[string]BackupChoice{"db": {Storage: "nas"}} },
		"revision": func(h *HashInput) { h.Revision = 2 },
	} {
		other := in
		edit(&other)
		if RequestHash(other) == RequestHash(in) {
			t.Fatalf("a different %s hashed the same", name)
		}
	}
	if RequestHash(HashInput{}) != RequestHash(HashInput{Values: map[string]string{}, Hosts: []model.InstanceHost{}}) {
		t.Fatal("nil and empty hash differently")
	}
}
