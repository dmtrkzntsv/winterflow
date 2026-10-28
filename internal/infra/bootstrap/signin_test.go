package bootstrap

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func TestSignInProvidersRefuseAPlaintextOIDCIssuer(t *testing.T) {
	for _, tc := range []struct {
		issuer string
		want   bool
	}{
		{"https://id.example.com/realms/home", true},
		{"http://localhost:8080/realms/home", true},
		{"http://127.0.0.1:8080", true},
		{"http://id.example.com/realms/home", false},
		{"id.example.com", false},
		{"https://user:pw@id.example.com", false},
	} {
		t.Run(tc.issuer, func(t *testing.T) {
			t.Setenv("WEB_URL", "https://wf.example.com")
			t.Setenv("AUTH_OIDC_ISSUER", tc.issuer)
			t.Setenv("AUTH_OIDC_CLIENT_ID", "cid")
			t.Setenv("AUTH_OIDC_CLIENT_SECRET", "secret")
			var buf bytes.Buffer
			log := &logger.Logger{Logger: slog.New(slog.NewTextHandler(&buf, nil))}
			cfg := config.NewServerConfig("standalone")
			_, providers := signInProvidersFromConfig(cfg, log)
			got := false
			for _, p := range providers {
				if p.ID == model.ProviderOIDC {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("OIDC provider enabled = %v, want %v (log: %s)", got, tc.want, buf.String())
			}
			if !tc.want && !strings.Contains(buf.String(), "AUTH_OIDC_ISSUER") {
				t.Fatalf("a refused issuer must be logged naming AUTH_OIDC_ISSUER, got %q", buf.String())
			}
		})
	}
}
