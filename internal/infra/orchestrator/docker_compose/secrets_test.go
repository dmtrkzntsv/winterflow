package dockercompose

import (
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	return NewRepository(
		config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	)
}

func TestResolveItemsPlaceholderPreservesPriorValue(t *testing.T) {
	r := newTestRepo(t)
	prev := map[string]string{"API_KEY": "stored-secret"}

	got := r.resolveItems([]command.ContentItem{
		{Name: "API_KEY", Encrypted: true, Content: []byte(command.EncryptedPlaceholder)},
		{Name: "PORT", Content: []byte("8080")},
	}, prev)

	byName := map[string]string{}
	for _, it := range got {
		byName[it.name] = string(it.content)
	}
	if byName["API_KEY"] != "stored-secret" {
		t.Errorf("placeholder did not preserve prior secret: got %q", byName["API_KEY"])
	}
	if byName["PORT"] != "8080" {
		t.Errorf("plaintext var = %q, want 8080", byName["PORT"])
	}
}

func TestResolveItemsPlaceholderWithNoPriorIsSkipped(t *testing.T) {
	r := newTestRepo(t)
	got := r.resolveItems([]command.ContentItem{
		{Name: "API_KEY", Encrypted: true, Content: []byte(command.EncryptedPlaceholder)},
	}, map[string]string{})
	if len(got) != 0 {
		t.Errorf("expected placeholder with no prior to be skipped, got %+v", got)
	}
}

func TestResolveItemsPlaintextPassthrough(t *testing.T) {
	r := newTestRepo(t)
	got := r.resolveItems([]command.ContentItem{
		{Name: "compose.yml", Content: []byte("services: {}")},
	}, map[string]string{})
	if len(got) != 1 || string(got[0].content) != "services: {}" {
		t.Errorf("plaintext file not passed through: %+v", got)
	}
}

func TestResolveItemsUndecryptableSecretIsSkipped(t *testing.T) {
	r := newTestRepo(t)
	// Encrypted content that is not the placeholder must be decrypted with the
	// agent key; garbage input fails and the item is dropped rather than
	// poisoning the render.
	got := r.resolveItems([]command.ContentItem{
		{Name: "API_KEY", Encrypted: true, Content: []byte("not-a-valid-ecies-payload")},
		{Name: "PORT", Content: []byte("8080")},
	}, map[string]string{})
	if len(got) != 1 || got[0].name != "PORT" {
		t.Errorf("expected only the plaintext item to survive, got %+v", got)
	}
}

func TestResolveItemsFallsBackToID(t *testing.T) {
	r := newTestRepo(t)
	// Older payloads keyed items on ID with no Name.
	got := r.resolveItems([]command.ContentItem{
		{ID: "legacy-id", Content: []byte("value")},
	}, map[string]string{})
	if len(got) != 1 || got[0].name != "legacy-id" {
		t.Errorf("expected name to fall back to ID, got %+v", got)
	}
}

func TestEncryptedNamesParsesConfig(t *testing.T) {
	cfg := []byte(`{
		"files":[{"filename":"compose.yml","is_encrypted":false},{"filename":"secret.env","is_encrypted":true}],
		"variables":[{"name":"PORT","is_encrypted":false},{"name":"API_KEY","is_encrypted":true}]
	}`)
	vars, files := encryptedNames(cfg)
	if !vars["API_KEY"] || vars["PORT"] {
		t.Errorf("variable encryption flags wrong: %+v", vars)
	}
	if !files["secret.env"] || files["compose.yml"] {
		t.Errorf("file encryption flags wrong: %+v", files)
	}
}

func TestEncryptedNamesInvalidConfig(t *testing.T) {
	vars, files := encryptedNames([]byte("not json"))
	if len(vars) != 0 || len(files) != 0 {
		t.Errorf("expected empty maps for unparseable config, got %v %v", vars, files)
	}
}
