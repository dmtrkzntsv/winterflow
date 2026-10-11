package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
)

// publicKeyOf calls GetPublicKey for serverID and returns data.public_key.
func publicKeyOf(t *testing.T, h *Handler, userID, serverID string) string {
	t.Helper()
	r := authedAs(httptest.NewRequest("GET", "/api/v1/server/get-public-key?server_id="+serverID, nil), userID)
	w := httptest.NewRecorder()
	h.GetPublicKey(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data PublicKeyResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data.PublicKey
}

func TestGetPublicKeyFromCapability(t *testing.T) {
	h, userID, serverID, _ := newEnv(t)
	if err := h.servers.SaveCapabilities(context.Background(), serverID, map[string]string{"public_key": "BASE64POINT"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := publicKeyOf(t, h, userID, serverID); got != "BASE64POINT" {
		t.Fatalf("public_key = %q, want the capability value", got)
	}
}

func TestGetPublicKeyFallsBackToAgentCert(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HUB_CERT_DIR", dir)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "winterflow-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "agent.crt"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	h, userID, serverID, _ := newEnv(t)
	if got := publicKeyOf(t, h, userID, serverID); got == "" {
		t.Fatal("public_key is empty; want the agent certificate's point")
	}
}

func TestRegisterClaimsPendingServer(t *testing.T) {
	h, userID, _, _ := newEnv(t)
	err := h.servers.RegisterServer(context.Background(), dto.ServerRegistrationDTO{
		ServerID:             "srv-2",
		CertificateID:        "cert-2",
		Hostname:             "box2",
		Code:                 "654321",
		ExpiresAt:            time.Now().Add(time.Hour),
		Certificate:          []byte("PEM"),
		CertificateExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	r := authedAs(httptest.NewRequest("POST", "/api/v1/server/register", strings.NewReader(`{"code":"654321"}`)), userID)
	w := httptest.NewRecorder()
	h.Register(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data RegisterServerResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Server.ID != "srv-2" {
		t.Fatalf("server = %+v, want srv-2", body.Data.Server)
	}
}
