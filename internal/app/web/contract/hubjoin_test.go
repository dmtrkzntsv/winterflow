package contract_test

import (
	"crypto/elliptic"
	"net/http"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/hubjoin"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
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

	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: "wfh_unknown", CSR: csr, PublicAddr: "hub-02.test:443"}).expect(t, http.StatusBadRequest)
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: "garbage", PublicAddr: "hub-02.test:443"}).expect(t, http.StatusBadRequest)

	var out hubjoin.EnrollResponse
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-02.test:443"}).
		expect(t, http.StatusOK).data(t, &out)
	if out.Certificate == "" || out.CACertificate == "" {
		t.Fatalf("response = %+v", out)
	}
	hub.post(t, "/api/v1/hub/enroll", hubjoin.EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-02.test:443"}).expect(t, http.StatusBadRequest)
}
