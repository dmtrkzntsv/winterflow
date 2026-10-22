package contract_test

import (
	"crypto/elliptic"
	"net/http"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/hubjoin"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	certpkg "winterflow/pkg/cert"
)

func TestHubEnrollRoute(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	hub := e.anon() // a hub has no account, only a token
	token, _, err := e.deps.HubJoin.Issue(e.dbCtx(t), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csr := enrolltest.CSR(t, elliptic.P256())

	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: "wfh_unknown", CSR: csr, PublicAddr: "hub-03.test:443"}).expect(t, http.StatusBadRequest)
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: "garbage", PublicAddr: "hub-03.test:443"}).expect(t, http.StatusBadRequest)
	// hub-02.test is live: its id cannot be taken (and the token survives).
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: csr, PublicAddr: "HUB-02.test:443"}).expect(t, http.StatusBadRequest)

	var out hubjoin.EnrollResponse
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-03.test:443"}).
		expect(t, http.StatusOK).data(t, &out)
	if out.Certificate == "" || out.CACertificate == "" || out.RedisUsername != "hub:hub-03.test" || out.RedisPassword == "" {
		t.Fatalf("response = %+v", out)
	}
	// The issued certificate is trusted: agents get it in their pin set.
	fp, err := certpkg.Fingerprint(out.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := e.deps.HubCertRepository.Active(e.dbCtx(t)); err != nil || len(active) != 1 || active[0] != fp {
		t.Fatalf("active = %v, %v; want [%s]", active, err, fp)
	}
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-03.test:443"}).expect(t, http.StatusBadRequest)
}
