package dockercompose

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// newSecretRepo builds a Repository whose agent key exists on disk, plus the
// matching public key for encrypting test secrets (mirrors pkg/crypto tests).
func newSecretRepo(t *testing.T) (*Repository, *ecdsa.PublicKey) {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	certDir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", certDir)

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "agent.key"),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}

	r := NewRepository(
		config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	)
	return r, &key.PublicKey
}

// eciesEncrypt mirrors the browser's encryption (ephemeral P-256 + ECDH +
// SHA-256 + AES-256-GCM) — same construction as pkg/crypto's tests.
func eciesEncrypt(t *testing.T, pub *ecdsa.PublicKey, plaintext string) string {
	t.Helper()
	curve := elliptic.P256()
	eph, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sharedX, _ := curve.ScalarMult(pub.X, pub.Y, eph.D.Bytes())
	xb := sharedX.Bytes()
	for len(xb) < 32 {
		xb = append([]byte{0}, xb...)
	}
	keyHash := sha256.Sum256(xb)
	block, _ := aes.NewCipher(keyHash[:])
	gcm, _ := cipher.NewGCM(block)
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, iv, []byte(plaintext), nil)
	ephPoint := elliptic.Marshal(curve, eph.X, eph.Y) //nolint:staticcheck
	payload := append(append(append([]byte{}, ephPoint...), iv...), ct...)
	return base64.StdEncoding.EncodeToString(payload)
}

func storePayload(pub *ecdsa.PublicKey, t *testing.T) command.AppPayload {
	t.Helper()
	return command.AppPayload{
		AppID:  "app-1",
		Config: []byte(`{"name":"demo","files":[{"filename":"compose.yml","is_encrypted":false},{"filename":"certs/tls.key","is_encrypted":true}],"variables":[{"name":"PORT","is_encrypted":false},{"name":"DB_PASS","is_encrypted":true}]}`),
		Files: []command.ContentItem{
			{Name: "compose.yml", Content: []byte("services: {}\n")},
			{Name: "certs/tls.key", Encrypted: true, Content: []byte(eciesEncrypt(t, pub, "PRIVATE-KEY"))},
		},
		Variables: []command.ContentItem{
			{Name: "PORT", Content: []byte("8080")},
			{Name: "DB_PASS", Encrypted: true, Content: []byte(eciesEncrypt(t, pub, "s3cret"))},
		},
	}
}

func TestWriteAppStoreLayout(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := r.writeAppStore(dir, storePayload(pub, t))
	if err != nil {
		t.Fatal(err)
	}

	// Plain file verbatim.
	if got, _ := os.ReadFile(filepath.Join(dir, "compose.yml")); string(got) != "services: {}\n" {
		t.Fatalf("compose.yml = %q", got)
	}
	// .env holds only the plain variable.
	env, _ := os.ReadFile(filepath.Join(dir, envRel))
	if string(env) != "PORT=8080\n" {
		t.Fatalf(".env = %q", env)
	}
	// secrets.json holds ciphertext, never plaintext.
	rawSecrets, _ := os.ReadFile(filepath.Join(dir, secretsRel))
	if strings.Contains(string(rawSecrets), "s3cret") || strings.Contains(string(rawSecrets), "PRIVATE-KEY") {
		t.Fatal("plaintext leaked into secrets.json")
	}
	var s secretStore
	if err := json.Unmarshal(rawSecrets, &s); err != nil {
		t.Fatal(err)
	}
	if s.Variables["DB_PASS"] == "" || s.Files["certs/tls.key"] == "" {
		t.Fatalf("secret store incomplete: %+v", s)
	}
	if store.Variables["DB_PASS"] != s.Variables["DB_PASS"] {
		t.Fatal("returned store differs from persisted store")
	}
	// The secret file must NOT exist as a committed plain file yet.
	if _, err := os.Stat(filepath.Join(dir, "certs", "tls.key")); !os.IsNotExist(err) {
		t.Fatal("secret file materialized during write (must happen only at deploy)")
	}
	// .gitignore covers the secret outputs.
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".env.secrets") || !strings.Contains(string(gi), "certs/tls.key") {
		t.Fatalf(".gitignore = %q", gi)
	}
	// config.json round-trips.
	cfg, raw, err := r.readAppConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["name"] != "demo" || len(raw) == 0 {
		t.Fatalf("readAppConfig = %v", cfg)
	}
}

func TestWriteAppStorePlaceholderKeepsCiphertext(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)

	first, err := r.writeAppStore(dir, storePayload(pub, t))
	if err != nil {
		t.Fatal(err)
	}

	// Edit round: unchanged secrets arrive as the placeholder.
	p := storePayload(pub, t)
	p.Variables[1].Content = []byte(command.EncryptedPlaceholder)
	p.Files[1].Content = []byte(command.EncryptedPlaceholder)
	second, err := r.writeAppStore(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	if second.Variables["DB_PASS"] != first.Variables["DB_PASS"] {
		t.Fatal("placeholder must preserve the previous variable ciphertext")
	}
	if second.Files["certs/tls.key"] != first.Files["certs/tls.key"] {
		t.Fatal("placeholder must preserve the previous file ciphertext")
	}
}

func TestWriteAppStoreRemovesStalePlainFiles(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)

	p := storePayload(pub, t)
	p.Config = []byte(`{"name":"demo","files":[{"filename":"compose.yml","is_encrypted":false},{"filename":"extra.conf","is_encrypted":false}],"variables":[]}`)
	p.Files = append(p.Files[:1], command.ContentItem{Name: "extra.conf", Content: []byte("x")})
	p.Variables = nil
	if _, err := r.writeAppStore(dir, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.conf")); err != nil {
		t.Fatal("extra.conf should exist after first save")
	}

	// Second save drops extra.conf from the config.
	p2 := storePayload(pub, t)
	p2.Config = []byte(`{"name":"demo","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]}`)
	p2.Files = p2.Files[:1]
	p2.Variables = nil
	if _, err := r.writeAppStore(dir, p2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extra.conf")); !os.IsNotExist(err) {
		t.Fatal("stale plain file should be removed")
	}
}

func TestWriteAppStoreRejectsPathTraversal(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)

	p := storePayload(pub, t)
	p.Files = []command.ContentItem{{Name: "../escape.txt", Content: []byte("nope")}}
	if _, err := r.writeAppStore(dir, p); err == nil {
		t.Fatal("expected error for path traversal filename")
	}
}

func TestMaterializeSecrets(t *testing.T) {
	r, pub := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)

	store, err := r.writeAppStore(dir, storePayload(pub, t))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.materializeSecrets(dir, store); err != nil {
		t.Fatal(err)
	}

	sec := parseEnv(mustRead(t, filepath.Join(dir, envSecretsRel)))
	if sec["DB_PASS"] != "s3cret" {
		t.Fatalf(".env.secrets = %v", sec)
	}
	if got := mustRead(t, filepath.Join(dir, "certs", "tls.key")); string(got) != "PRIVATE-KEY" {
		t.Fatalf("secret file = %q", got)
	}
}

func TestMaterializeSecretsSkipsUndecryptable(t *testing.T) {
	r, _ := newSecretRepo(t)
	dir := filepath.Join(r.cfg.GetAppsDataDir(), "app-1")
	_ = os.MkdirAll(dir, 0o755)

	err := r.materializeSecrets(dir, secretStore{
		Variables: map[string]string{"BAD": "not-real-ciphertext"},
		Files:     map[string]string{},
	})
	if err != nil {
		t.Fatalf("undecryptable entries must be skipped, not fatal: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, envSecretsRel)); !os.IsNotExist(statErr) {
		t.Fatal("no decryptable vars -> no .env.secrets file")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
