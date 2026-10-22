package contract_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net/http"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/agenthub"
	"winterflow/internal/app/web/handler/enroll"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/pkg/agentsig"
)

func TestAgentHubRoute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	agent := e.anon() // the agent's signature is its only credential
	owner := e.claim(t)

	// Enroll an agent whose key the test holds.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "box"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	var started enroll.CodeResponse
	agent.post(t, "/api/v1/agent/enroll", enroll.StartRequest{CSR: csr, Hostname: "box"}).expect(t, http.StatusOK).data(t, &started)
	var reg server.RegisterServerResponse
	owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: started.Code}).expect(t, http.StatusOK).data(t, &reg)
	serverID := reg.Server.ID

	ask := func(ts int64, nonce string, signer *ecdsa.PrivateKey) *response {
		t.Helper()
		sig, err := agentsig.Sign(signer, agentsig.Payload(serverID, ts, nonce))
		if err != nil {
			t.Fatal(err)
		}
		return agent.do(t, call{Method: http.MethodPost, Path: "/api/v1/agent/hub",
			Body:   agenthub.WhereRequest{ServerID: serverID, Timestamp: ts, Nonce: nonce},
			Header: http.Header{agentsig.Header: {sig}}})
	}

	// Its hub is live: stay. Nothing trusted yet: an empty set.
	var out agenthub.WhereResponse
	ask(time.Now().Unix(), "n0", key).expect(t, http.StatusOK).data(t, &out)
	if out.HubFingerprints == nil || len(out.HubFingerprints) != 0 {
		t.Fatalf("no pins: %+v", out)
	}
	const pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := e.deps.HubCertRepository.Trust(e.dbCtx(t), enrolltest.DefaultHub.ID, pin, time.Now()); err != nil {
		t.Fatal(err)
	}
	ask(time.Now().Unix(), "n1", key).expect(t, http.StatusOK).data(t, &out)
	if out.HubAddr != enrolltest.DefaultHub.PublicAddr || out.Moved || len(out.HubFingerprints) != 1 || out.HubFingerprints[0] != pin {
		t.Fatalf("stay: %+v", out)
	}

	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ask(time.Now().Unix(), "n2", other).expect(t, http.StatusUnauthorized)
	ask(time.Now().Add(-time.Hour).Unix(), "n3", key).expect(t, http.StatusBadRequest)

	// Its hub drains: the server is moved to the other one, and stays there.
	e.hubs.setDraining(enrolltest.DefaultHub.ID)
	ask(time.Now().Unix(), "n4", key).expect(t, http.StatusOK).data(t, &out)
	if out.HubAddr != "hub-02.test:443" || !out.Moved || len(out.HubFingerprints) != 1 {
		t.Fatalf("moved: %+v", out)
	}
	if hub, err := e.deps.ServerRepository.ServerHub(e.dbCtx(t), serverID); err != nil || hub != "hub-02.test" {
		t.Fatalf("stored hub %q, %v", hub, err)
	}
	ask(time.Now().Unix(), "n5", key).expect(t, http.StatusOK).data(t, &out)
	if out.HubAddr != "hub-02.test:443" || out.Moved {
		t.Fatalf("after the move: %+v", out)
	}
}
