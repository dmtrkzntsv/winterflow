package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// unsetenv guarantees key is absent for the duration of the test, restoring
// the original value afterwards (t.Setenv registers the restore cleanup).
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}

func TestIsStandalone(t *testing.T) {
	if !NewServerConfig("standalone").IsStandalone() {
		t.Fatal("standalone mode: IsStandalone() = false, want true")
	}
	if NewServerConfig("distributed").IsStandalone() {
		t.Fatal("distributed mode: IsStandalone() = true, want false")
	}
}

func TestSimpleEnvGetters(t *testing.T) {
	c := NewServerConfig("standalone")
	tests := []struct {
		name string
		env  string
		get  func() string
	}{
		{"GetLogLevel", "LOG_LEVEL", c.GetLogLevel},
		{"GetApiPort", "API_PORT", c.GetApiPort},
		{"GetWebURL", "WEB_URL", c.GetWebURL},
		{"GetJwtSecret", "JWT_SECRET", c.GetJwtSecret},
		{"GetHubHost", "HUB_HOST", c.GetHubHost},
		{"GetHubPort", "HUB_PORT", c.GetHubPort},
		{"GetHubCertDir", "HUB_CERT_DIR", c.GetHubCertDir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unsetenv(t, tt.env)
			if got := tt.get(); got != "" {
				t.Fatalf("%s with %s unset = %q, want empty", tt.name, tt.env, got)
			}
			t.Setenv(tt.env, "some-value")
			if got := tt.get(); got != "some-value" {
				t.Fatalf("%s = %q, want %q", tt.name, tt.get(), "some-value")
			}
		})
	}
}

func TestGetSecureCookies(t *testing.T) {
	c := NewServerConfig("standalone")
	tests := []struct {
		webURL string
		want   bool
	}{
		{"https://app.example.com", true},
		{"http://192.168.1.10:5173", false},
		{"http://localhost:5173", false},
		{"", false},
		{"ftp://example.com", false},
		{"HTTPS://example.com", false}, // scheme comparison is exact
	}
	for _, tt := range tests {
		t.Run(tt.webURL, func(t *testing.T) {
			if tt.webURL == "" {
				unsetenv(t, "WEB_URL")
			} else {
				t.Setenv("WEB_URL", tt.webURL)
			}
			if got := c.GetSecureCookies(); got != tt.want {
				t.Fatalf("GetSecureCookies() with WEB_URL=%q = %v, want %v", tt.webURL, got, tt.want)
			}
		})
	}
}

func TestGetAllowedOrigins(t *testing.T) {
	c := NewServerConfig("standalone")
	unsetenv(t, "CORS_ALLOW_ORIGINS")
	if got := c.GetAllowedOrigins(); got != "*" {
		t.Fatalf("default GetAllowedOrigins() = %q, want *", got)
	}
	t.Setenv("CORS_ALLOW_ORIGINS", "https://a.com,https://b.com")
	if got := c.GetAllowedOrigins(); got != "https://a.com,https://b.com" {
		t.Fatalf("GetAllowedOrigins() = %q, want the env value", got)
	}
}

func TestGetRedisCredentials(t *testing.T) {
	c := NewServerConfig("distributed")

	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "s3cret")
	t.Setenv("REDIS_DB", "3")
	addr, pass, db := c.GetRedisCredentials()
	if addr != "redis:6379" || pass != "s3cret" || db != 3 {
		t.Fatalf("GetRedisCredentials() = (%q, %q, %d), want (redis:6379, s3cret, 3)", addr, pass, db)
	}

	// Unset or malformed REDIS_DB falls back to db 0.
	for _, bad := range []string{"", "not-a-number", "3.5"} {
		if bad == "" {
			unsetenv(t, "REDIS_DB")
		} else {
			t.Setenv("REDIS_DB", bad)
		}
		if _, _, db := c.GetRedisCredentials(); db != 0 {
			t.Fatalf("REDIS_DB=%q: db = %d, want 0", bad, db)
		}
	}
}

func TestAgentDataDirs(t *testing.T) {
	c := NewServerConfig("standalone")

	unsetenv(t, "AGENT_DATA_DIR")
	if got := c.GetAgentDataDir(); got != "data" {
		t.Fatalf("default GetAgentDataDir() = %q, want data", got)
	}
	if got := c.GetAppsDir(); got != "data/apps" {
		t.Fatalf("default GetAppsDir() = %q, want data/apps", got)
	}

	t.Setenv("AGENT_DATA_DIR", "/opt/winterflow")
	if got := c.GetAgentDataDir(); got != "/opt/winterflow" {
		t.Fatalf("GetAgentDataDir() = %q, want /opt/winterflow", got)
	}
	if got := c.GetAppsDir(); got != "/opt/winterflow/apps" {
		t.Fatalf("GetAppsDir() = %q, want /opt/winterflow/apps", got)
	}
}

func TestGetBackupRetentionDays(t *testing.T) {
	c := NewServerConfig("standalone")
	for v, want := range map[string]int{"": 180, "30": 30, "0": 0, "-1": 180, "a year": 180, "36500": 36500, "36501": 36500, "213504": 36500, "99999999": 36500} {
		t.Setenv("BACKUP_RETENTION_DAYS", v)
		if got := c.GetBackupRetentionDays(); got != want {
			t.Errorf("BACKUP_RETENTION_DAYS=%q: %d, want %d", v, got, want)
		}
	}
	unsetenv(t, "BACKUP_RETENTION_DAYS")
	if got := c.GetBackupRetentionDays(); got != 180 {
		t.Fatalf("unset: %d", got)
	}
}

func TestGetExposePortRange(t *testing.T) {
	c := NewServerConfig("standalone")
	for v, want := range map[string][2]int{
		"": {20000, 29999}, "30000-30099": {30000, 30099}, " 1 - 65535 ": {1, 65535}, "5000-5000": {5000, 5000},
		"30000": {20000, 29999}, "a-b": {20000, 29999}, "0-10": {20000, 29999}, "10-70000": {20000, 29999}, "300-200": {20000, 29999},
	} {
		t.Setenv("EXPOSE_PORT_RANGE", v)
		if lo, hi := c.GetExposePortRange(); lo != want[0] || hi != want[1] {
			t.Errorf("EXPOSE_PORT_RANGE=%q: %d-%d, want %d-%d", v, lo, hi, want[0], want[1])
		}
	}
	unsetenv(t, "EXPOSE_PORT_RANGE")
	if lo, hi := c.GetExposePortRange(); lo != 20000 || hi != 29999 {
		t.Fatalf("unset: %d-%d", lo, hi)
	}
}

func TestCertPathsDeriveFromCertDir(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("HUB_CERT_DIR", "/etc/wf/certs")
	tests := []struct {
		name string
		get  func() string
		want string
	}{
		{"hub CA cert", c.GetHubCACertPath, "/etc/wf/certs/ca.crt"},
		{"hub CA key", c.GetHubCAKeyPath, "/etc/wf/certs/ca.key"},
		{"hub cert", c.GetHubCertPath, "/etc/wf/certs/hub.crt"},
		{"hub key", c.GetHubKeyPath, "/etc/wf/certs/hub.key"},
		{"agent cert", c.GetAgentCertPath, "/etc/wf/certs/agent.crt"},
		{"agent key", c.GetAgentKeyPath, "/etc/wf/certs/agent.key"},
	}
	for _, tt := range tests {
		if got := tt.get(); got != tt.want {
			t.Fatalf("%s = %q, want %q", tt.name, tt.get(), tt.want)
		}
	}
}

func TestGetAgentCACertPath(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("HUB_CERT_DIR", "/etc/wf/certs")

	// Default: same CA the hub uses.
	unsetenv(t, "AGENT_CA_CERT_PATH")
	if got := c.GetAgentCACertPath(); got != "/etc/wf/certs/ca.crt" {
		t.Fatalf("default GetAgentCACertPath() = %q, want /etc/wf/certs/ca.crt", got)
	}

	// Explicit override for deployments shipping the CA separately.
	t.Setenv("AGENT_CA_CERT_PATH", "/tmp/other-ca.crt")
	if got := c.GetAgentCACertPath(); got != "/tmp/other-ca.crt" {
		t.Fatalf("override GetAgentCACertPath() = %q, want /tmp/other-ca.crt", got)
	}
}

func TestIsAuthSupported(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		provider string
		env      map[string]string
		want     bool
	}{
		{
			name:     "google enabled in standalone with credentials",
			mode:     "standalone",
			provider: "google",
			env:      map[string]string{"AUTH_GOOGLE_CLIENT_ID": "id", "AUTH_GOOGLE_CLIENT_SECRET": "sec"},
			want:     true,
		},
		{
			name:     "google enabled in distributed with credentials",
			mode:     "distributed",
			provider: "google",
			env:      map[string]string{"AUTH_GOOGLE_CLIENT_ID": "id", "AUTH_GOOGLE_CLIENT_SECRET": "sec"},
			want:     true,
		},
		{
			name:     "google disabled in distributed with missing secret",
			mode:     "distributed",
			provider: "google",
			env:      map[string]string{"AUTH_GOOGLE_CLIENT_ID": "id"},
			want:     false,
		},
		{
			name:     "env provider no longer exists",
			mode:     "standalone",
			provider: "env",
			env:      map[string]string{"AUTH_ENV_USERNAME": "admin", "AUTH_ENV_PASSWORD": "pw"},
			want:     false,
		},
		{
			name:     "unknown provider unsupported",
			mode:     "standalone",
			provider: "github",
			want:     false,
		},
	}
	authVars := []string{"AUTH_GOOGLE_CLIENT_ID", "AUTH_GOOGLE_CLIENT_SECRET", "AUTH_ENV_USERNAME", "AUTH_ENV_PASSWORD"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range authVars {
				unsetenv(t, k)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			c := NewServerConfig(tt.mode)
			if got := c.IsAuthSupported(tt.provider); got != tt.want {
				t.Fatalf("IsAuthSupported(%q) in %s mode = %v, want %v", tt.provider, tt.mode, got, tt.want)
			}
		})
	}
}

func TestGetAuthCredentialPairs(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("AUTH_GOOGLE_CLIENT_ID", "gid")
	t.Setenv("AUTH_GOOGLE_CLIENT_SECRET", "gsec")
	if id, sec := c.GetGoogleAuth(); id != "gid" || sec != "gsec" {
		t.Fatalf("GetGoogleAuth() = (%q, %q), want (gid, gsec)", id, sec)
	}
}

func TestGetGitHubReleasesURL(t *testing.T) {
	c := NewServerConfig("standalone")
	unsetenv(t, "GITHUB_RELEASES_URL")
	want := "https://github.com/dmtrkzntsv/winterflow/releases/download"
	if got := c.GetGitHubReleasesURL(); got != want {
		t.Fatalf("default GetGitHubReleasesURL() = %q, want %q", got, want)
	}
	t.Setenv("GITHUB_RELEASES_URL", "https://mirror.internal/releases")
	if got := c.GetGitHubReleasesURL(); got != "https://mirror.internal/releases" {
		t.Fatalf("override GetGitHubReleasesURL() = %q", got)
	}
}

func TestIsRegistrationEnabled(t *testing.T) {
	c := NewServerConfig("standalone")
	cases := map[string]bool{"": true, "true": true, "1": true, "false": false, "0": false, "FALSE": false}
	for val, want := range cases {
		if val == "" {
			unsetenv(t, "REGISTRATION_ENABLED")
		} else {
			t.Setenv("REGISTRATION_ENABLED", val)
		}
		if got := c.IsRegistrationEnabled(); got != want {
			t.Errorf("REGISTRATION_ENABLED=%q: got %v, want %v", val, got, want)
		}
	}
}

func TestIsIngressEnabled(t *testing.T) {
	c := NewServerConfig("standalone")
	// Opt-in: only an explicit true/1 turns the embedded proxy on.
	cases := map[string]bool{"": false, "true": true, "1": true, "TRUE": true, "false": false, "0": false, "yes": false}
	for val, want := range cases {
		if val == "" {
			unsetenv(t, "INGRESS_ENABLED")
		} else {
			t.Setenv("INGRESS_ENABLED", val)
		}
		if got := c.IsIngressEnabled(); got != want {
			t.Errorf("INGRESS_ENABLED=%q: got %v, want %v", val, got, want)
		}
	}
}

func TestGetIngressPorts(t *testing.T) {
	c := NewServerConfig("standalone")
	tests := []struct {
		name string
		env  string
		get  func() int
		val  string // "" means unset
		want int
	}{
		{"http unset defaults to 80", "INGRESS_HTTP_PORT", c.GetIngressHTTPPort, "", 80},
		{"http override", "INGRESS_HTTP_PORT", c.GetIngressHTTPPort, "8080", 8080},
		{"http malformed falls back to 80", "INGRESS_HTTP_PORT", c.GetIngressHTTPPort, "junk", 80},
		{"https unset defaults to 443", "INGRESS_HTTPS_PORT", c.GetIngressHTTPSPort, "", 443},
		{"https override", "INGRESS_HTTPS_PORT", c.GetIngressHTTPSPort, "8443", 8443},
		{"https malformed falls back to 443", "INGRESS_HTTPS_PORT", c.GetIngressHTTPSPort, "junk", 443},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.val == "" {
				unsetenv(t, tt.env)
			} else {
				t.Setenv(tt.env, tt.val)
			}
			if got := tt.get(); got != tt.want {
				t.Errorf("%s=%q: got %d, want %d", tt.env, tt.val, got, tt.want)
			}
		})
	}
}

func TestGetIngressACMEEmail(t *testing.T) {
	c := NewServerConfig("standalone")
	unsetenv(t, "INGRESS_ACME_EMAIL")
	if got := c.GetIngressACMEEmail(); got != "" {
		t.Fatalf("unset GetIngressACMEEmail() = %q, want empty", got)
	}
	t.Setenv("INGRESS_ACME_EMAIL", "ops@example.com")
	if got := c.GetIngressACMEEmail(); got != "ops@example.com" {
		t.Fatalf("GetIngressACMEEmail() = %q, want ops@example.com", got)
	}
}

func TestGetIngressTrustedProxies(t *testing.T) {
	c := NewServerConfig("standalone")
	unsetenv(t, "INGRESS_TRUSTED_PROXIES")
	if got := c.GetIngressTrustedProxies(); len(got) != 0 {
		t.Fatalf("unset = %v, want none", got)
	}
	t.Setenv("INGRESS_TRUSTED_PROXIES", " 172.16.0.0/12, ,10.0.0.1,")
	got := c.GetIngressTrustedProxies()
	if len(got) != 2 || got[0] != "172.16.0.0/12" || got[1] != "10.0.0.1" {
		t.Fatalf("GetIngressTrustedProxies() = %q", got)
	}
}

func TestIsMetricsEnabled(t *testing.T) {
	c := NewServerConfig("standalone")
	cases := map[string]bool{"": true, "true": true, "1": true, "false": false, "FALSE": false, "0": false, "off": false, "junk": true}
	for val, want := range cases {
		if val == "" {
			unsetenv(t, "METRICS_ENABLED")
		} else {
			t.Setenv("METRICS_ENABLED", val)
		}
		if got := c.IsMetricsEnabled(); got != want {
			t.Errorf("METRICS_ENABLED=%q: got %v, want %v", val, got, want)
		}
	}
}

func TestGetMetricsDiskInterval(t *testing.T) {
	c := NewServerConfig("standalone")
	cases := map[string]time.Duration{
		"": time.Hour, "0": 0, "0s": 0, "2h": 2 * time.Hour, "30m": 30 * time.Minute,
		"1m": 5 * time.Minute, "junk": time.Hour, "-1h": time.Hour,
	}
	for val, want := range cases {
		if val == "" {
			unsetenv(t, "METRICS_DISK_INTERVAL")
		} else {
			t.Setenv("METRICS_DISK_INTERVAL", val)
		}
		if got := c.GetMetricsDiskInterval(); got != want {
			t.Errorf("METRICS_DISK_INTERVAL=%q: got %v, want %v", val, got, want)
		}
	}
}

func TestAllowsPrivateAlertTargets(t *testing.T) {
	standalone, api := NewServerConfig("standalone"), NewServerConfig("distributed")
	unsetenv(t, "ALERTS_ALLOW_PRIVATE_TARGETS")
	if !standalone.AllowsPrivateAlertTargets() || api.AllowsPrivateAlertTargets() {
		t.Fatal("defaults: standalone allows, distributed refuses")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "true")
	if !api.AllowsPrivateAlertTargets() {
		t.Error("true did not allow on the distributed API")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "off")
	if standalone.AllowsPrivateAlertTargets() {
		t.Error("off did not refuse in standalone")
	}
	t.Setenv("ALERTS_ALLOW_PRIVATE_TARGETS", "junk")
	if !standalone.AllowsPrivateAlertTargets() || api.AllowsPrivateAlertTargets() {
		t.Error("an unknown value must keep the topology default")
	}
}

func TestCatalogConfig(t *testing.T) {
	c := NewServerConfig("distributed")
	unsetenv(t, "CATALOG_URL")
	unsetenv(t, "CATALOG_PUBLIC_KEY")
	unsetenv(t, "CATALOG_REFRESH")
	if c.GetCatalogURL() != DefaultCatalogURL || c.GetCatalogPublicKey() != DefaultCatalogPublicKey || c.GetCatalogRefresh() != 24*time.Hour {
		t.Fatalf("defaults = %q %q %v", c.GetCatalogURL(), c.GetCatalogPublicKey(), c.GetCatalogRefresh())
	}
	// Set but empty switches the catalog off; an empty key is the default.
	t.Setenv("CATALOG_URL", "")
	t.Setenv("CATALOG_PUBLIC_KEY", " ")
	if c.GetCatalogURL() != "" || c.GetCatalogPublicKey() != DefaultCatalogPublicKey {
		t.Fatalf("empty = %q %q", c.GetCatalogURL(), c.GetCatalogPublicKey())
	}
	t.Setenv("CATALOG_URL", " http://127.0.0.1:8080/catalog.json ")
	t.Setenv("CATALOG_PUBLIC_KEY", "a2V5")
	if c.GetCatalogURL() != "http://127.0.0.1:8080/catalog.json" || c.GetCatalogPublicKey() != "a2V5" {
		t.Fatalf("set = %q %q", c.GetCatalogURL(), c.GetCatalogPublicKey())
	}
	for v, want := range map[string]time.Duration{
		"6h": 6 * time.Hour, "1m": 5 * time.Minute, "soon": 24 * time.Hour, "-1h": 24 * time.Hour, "0": 24 * time.Hour,
	} {
		t.Setenv("CATALOG_REFRESH", v)
		if got := c.GetCatalogRefresh(); got != want {
			t.Errorf("CATALOG_REFRESH=%s: %v, want %v", v, got, want)
		}
	}
}

func TestGetDbURLStandaloneDefault(t *testing.T) {
	unsetenv(t, "DATABASE_URL")
	if got := NewServerConfig("standalone").GetDbURL(); got != "sqlite://winterflow.sqlite" {
		t.Fatalf("standalone default = %q", got)
	}
	if got := NewServerConfig("distributed").GetDbURL(); got != "" {
		t.Fatalf("distributed unset = %q, want empty", got)
	}
	t.Setenv("DATABASE_URL", "postgres://x/y")
	if got := NewServerConfig("standalone").GetDbURL(); got != "postgres://x/y" {
		t.Fatalf("explicit = %q", got)
	}
}

func TestValidateDatabaseURL(t *testing.T) {
	tests := []struct {
		mode, url string
		wantErr   bool
	}{
		{"standalone", "sqlite://winterflow.sqlite", false},
		{"standalone", "postgres://u@h/db", false},
		{"standalone", "mysql://u@h/db", true},
		{"distributed", "postgres://u@h/db", false},
		{"distributed", "postgresql://u@h/db", false},
		{"distributed", "sqlite://x.sqlite", true},
		{"distributed", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.mode+"/"+tt.url, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.url)
			err := NewServerConfig(tt.mode).ValidateDatabaseURL()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func writeCA(t *testing.T, certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnrollment(t *testing.T) {
	c := NewServerConfig("distributed")

	dir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", dir)
	if err := c.ValidateEnrollment(); err == nil {
		t.Fatal("empty dir: want error")
	}

	writeCA(t, c.GetHubCACertPath(), c.GetHubCAKeyPath())
	if err := c.ValidateEnrollment(); err != nil {
		t.Fatalf("valid pair: %v", err)
	}

	if err := os.Remove(c.GetHubCAKeyPath()); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateEnrollment(); err == nil {
		t.Fatal("missing key: want error")
	}

	other := t.TempDir()
	writeCA(t, filepath.Join(other, "c.pem"), filepath.Join(other, "k.pem"))
	kb, _ := os.ReadFile(filepath.Join(other, "k.pem"))
	if err := os.WriteFile(c.GetHubCAKeyPath(), kb, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateEnrollment(); err == nil {
		t.Fatal("mismatched pair: want error")
	}
}

func TestHubConfig(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.winterflow.dev:50051")
	if got := c.GetHubPublicAddr(); got != "hub-01.winterflow.dev:50051" {
		t.Fatalf("addr = %q", got)
	}
	if got := c.GetHubID(); got != "hub-01.winterflow.dev" {
		t.Fatalf("id = %q", got)
	}
	t.Setenv("HUB_PUBLIC_ADDR", "Hub-01.WinterFlow.dev:50051")
	if got := c.GetHubID(); got != "hub-01.winterflow.dev" {
		t.Fatalf("mixed-case id = %q, want lower case", got)
	}
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.winterflow.dev:50051")
	if err := c.ValidateHub(); err != nil {
		t.Fatalf("ValidateHub: %v", err)
	}

	unsetenv(t, "HUB_CAPACITY")
	if got := c.GetHubCapacity(); got != 1000 {
		t.Fatalf("default capacity = %d", got)
	}
	t.Setenv("HUB_CAPACITY", "0")
	if got := c.GetHubCapacity(); got != 1000 {
		t.Fatalf("capacity 0 = %d", got)
	}
	t.Setenv("HUB_CAPACITY", "250")
	if got := c.GetHubCapacity(); got != 250 {
		t.Fatalf("capacity 250 = %d", got)
	}

	unsetenv(t, "HUB_ACCEPTING")
	if !c.GetHubAccepting() {
		t.Fatal("default accepting = false")
	}
	t.Setenv("HUB_ACCEPTING", "false")
	if c.GetHubAccepting() {
		t.Fatal("accepting=false ignored")
	}

	t.Setenv("HUB_PUBLIC_ADDR", "nohost")
	if err := c.ValidateHub(); err == nil {
		t.Fatal("nohost: want error")
	}
	t.Setenv("HUB_PUBLIC_ADDR", "")
	if err := c.ValidateHub(); err == nil {
		t.Fatal("empty: want error")
	}
}

func TestChannels(t *testing.T) {
	t.Setenv("REGION", "eu")
	t.Setenv("HUB_PUBLIC_ADDR", "hub-01.winterflow.dev:50051")
	c := NewServerConfig("distributed")
	checks := map[string][2]string{
		"request":   {c.GetBusRequestQueue(), "bus:requests"},
		"response":  {c.GetBusResponseQueue(), "bus:responses"},
		"events":    {c.GetBusEventsQueue(), "bus:events"},
		"hubqueue":  {c.HubRequestQueue(c.GetHubID()), "bus:requests:hub-01.winterflow.dev"},
		"hubreq":    {c.GetHubRequestsChannel(), "hub:requests"},
		"broadcast": {c.GetHubBroadcastChannel(), "hub:broadcast"},
		"hubresp":   {HubResponsesChannel("h1"), "hub:responses:h1"},
	}
	for name, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", name, v[0], v[1])
		}
	}
}

func TestRedisTLSDefaults(t *testing.T) {
	t.Setenv("REDIS_TLS", "")
	if !NewServerConfig("distributed").GetRedisTLS() {
		t.Fatal("distributed must default to TLS")
	}
	if NewServerConfig("standalone").GetRedisTLS() {
		t.Fatal("standalone must default to no TLS")
	}
	t.Setenv("REDIS_TLS", "false")
	if NewServerConfig("distributed").GetRedisTLS() {
		t.Fatal("REDIS_TLS=false must turn TLS off")
	}
	t.Setenv("REDIS_CA_CERT_PATH", "")
	c := NewServerConfig("distributed")
	if c.GetRedisCACertPath() != c.GetHubCACertPath() {
		t.Fatal("CA path must default to the hub CA")
	}
	t.Setenv("REDIS_CA_CERT_PATH", "/x/ca.pem")
	if c.GetRedisCACertPath() != "/x/ca.pem" {
		t.Fatal("REDIS_CA_CERT_PATH override ignored")
	}
}

func TestGetApiListenAddr(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("API_PORT", "8080")
	unsetenv(t, "API_LISTEN_ADDR")
	if got := c.GetApiListenAddr(); got != ":8080" {
		t.Fatalf("unset API_LISTEN_ADDR = %q, want :8080 (all interfaces)", got)
	}
	t.Setenv("API_LISTEN_ADDR", "127.0.0.1")
	if got := c.GetApiListenAddr(); got != "127.0.0.1:8080" {
		t.Fatalf("API_LISTEN_ADDR=127.0.0.1 = %q, want 127.0.0.1:8080", got)
	}
	t.Setenv("API_LISTEN_ADDR", " ::1 ")
	if got := c.GetApiListenAddr(); got != "[::1]:8080" {
		t.Fatalf("API_LISTEN_ADDR=::1 = %q, want [::1]:8080", got)
	}
}

func TestGetWinterflowAPIURL(t *testing.T) {
	c := NewServerConfig("distributed")
	t.Setenv("WINTERFLOW_API_URL", "")
	if got := c.GetWinterflowAPIURL(); got != "https://api.winterflow.dev" {
		t.Fatalf("default = %q", got)
	}
	t.Setenv("WINTERFLOW_API_URL", " https://api.example.com/ ")
	if got := c.GetWinterflowAPIURL(); got != "https://api.example.com" {
		t.Fatalf("set = %q", got)
	}
}

func TestGetRedisUsername(t *testing.T) {
	t.Setenv("REDIS_USERNAME", "")
	if got := NewServerConfig("distributed").GetRedisUsername(); got != "" {
		t.Fatalf("unset REDIS_USERNAME = %q, want empty (default user)", got)
	}
	t.Setenv("REDIS_USERNAME", "hub:hub-01.winterflow.dev")
	if got := NewServerConfig("distributed").GetRedisUsername(); got != "hub:hub-01.winterflow.dev" {
		t.Fatalf("GetRedisUsername() = %q", got)
	}
}
