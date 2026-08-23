package config

import (
	"os"
	"path/filepath"
	"testing"
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
		{"GetRegion", "REGION", c.GetRegion},
		{"GetLogLevel", "LOG_LEVEL", c.GetLogLevel},
		{"GetApiPort", "API_PORT", c.GetApiPort},
		{"GetWebURL", "WEB_URL", c.GetWebURL},
		{"GetDbURL", "DATABASE_URL", c.GetDbURL},
		{"GetJwtSecret", "JWT_SECRET", c.GetJwtSecret},
		{"GetHubHost", "HUB_HOST", c.GetHubHost},
		{"GetHubPort", "HUB_PORT", c.GetHubPort},
		{"GetHubCASubject", "HUB_CA_SUBJECT", c.GetHubCASubject},
		{"GetHubServerSubject", "HUB_SERVER_SUBJECT", c.GetHubServerSubject},
		{"GetHubCertExtPath", "HUB_CERT_EXT_PATH", c.GetHubCertExtPath},
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

func TestBusQueues(t *testing.T) {
	c := NewServerConfig("distributed")
	tests := []struct {
		name        string
		env         string
		get         func() string
		wantDefault string // with REGION=eu-1 and env unset
	}{
		{"request queue", "BUS_REQUEST_QUEUE", c.GetBusRequestQueue, "requests:eu-1"},
		{"response queue", "BUS_RESPONSE_QUEUE", c.GetBusResponseQueue, "responses:eu-1"},
		{"events queue", "BUS_EVENTS_QUEUE", c.GetBusEventsQueue, "events:eu-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("REGION", "eu-1")
			unsetenv(t, tt.env)
			if got := tt.get(); got != tt.wantDefault {
				t.Fatalf("default = %q, want %q (REGION-scoped)", got, tt.wantDefault)
			}

			// Explicit env overrides the region-derived default.
			t.Setenv(tt.env, "custom-queue")
			if got := tt.get(); got != "custom-queue" {
				t.Fatalf("override = %q, want custom-queue", got)
			}
		})
	}

	// Different regions must yield different queues (region isolation).
	unsetenv(t, "BUS_REQUEST_QUEUE")
	t.Setenv("REGION", "us-2")
	if got := c.GetBusRequestQueue(); got != "requests:us-2" {
		t.Fatalf("REGION=us-2: request queue = %q, want requests:us-2", got)
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
			name:     "google disabled in standalone even with credentials",
			mode:     "standalone",
			provider: "google",
			env:      map[string]string{"AUTH_GOOGLE_CLIENT_ID": "id", "AUTH_GOOGLE_CLIENT_SECRET": "sec"},
			want:     false,
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

func TestGetAvatarsStoragePath(t *testing.T) {
	c := NewServerConfig("standalone")

	// Explicit env value is returned verbatim, no directory creation.
	t.Setenv("AVATARS_STORAGE_PATH", "/does/not/exist/avatars")
	if got := c.GetAvatarsStoragePath(); got != "/does/not/exist/avatars" {
		t.Fatalf("GetAvatarsStoragePath() = %q, want the env value", got)
	}

	// Default: data/avatars under the working directory, created on demand.
	// Run from a temp dir so the repo tree is untouched.
	unsetenv(t, "AVATARS_STORAGE_PATH")
	t.Chdir(t.TempDir())
	got := c.GetAvatarsStoragePath()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	want := filepath.Join(cwd, "data/avatars")
	if got != want {
		t.Fatalf("default GetAvatarsStoragePath() = %q, want %q", got, want)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Fatalf("default avatars dir %q was not created: %v", got, err)
	}
}

func TestGetGitHubReleasesURL(t *testing.T) {
	c := NewServerConfig("standalone")
	unsetenv(t, "GITHUB_RELEASES_URL")
	want := "https://github.com/winterflowio/winterflow-agent/releases/download"
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
