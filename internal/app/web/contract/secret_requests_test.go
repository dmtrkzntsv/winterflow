package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	hsecretreq "winterflow/internal/app/web/handler/secretreq"
	"winterflow/pkg/crypto/cryptotest"
)

// TestSecretRequestRoutes: a member of the request's org views the link and
// submits ciphertexts once; a plaintext is 400; another org's member, a
// submitted link and an unknown token all read the same 404.
func TestSecretRequestRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	if err := e.deps.ServerRepository.SaveCapabilities(e.dbCtx(t), sid, map[string]string{"public_key": "BASE64POINT"}, nil); err != nil {
		t.Fatal(err)
	}
	req, raw, err := e.deps.SecretRequests.Create(e.dbCtx(t), owner.userID, sid, nil, []string{"API_KEY", "DB_PASSWORD"},
		map[string]string{"API_KEY": "from the dashboard"}, "for paperless")
	if err != nil {
		t.Fatal(err)
	}

	var v hsecretreq.RequestView
	owner.get(t, "/api/v1/secret-request/view", url.Values{"token": {raw}}).expect(t, http.StatusOK).data(t, &v)
	if v.ServerID != sid || v.PublicKey != "BASE64POINT" || len(v.Names) != 2 || v.Notes["API_KEY"] != "from the dashboard" ||
		v.Message != "for paperless" || v.AppID != "" || v.ExpiresAt.IsZero() {
		t.Fatalf("view = %+v", v)
	}

	// Another org's member sees what an unknown token shows.
	const otherEmail, otherPassword = "mallory@other.test", "password"
	if _, err := e.deps.UserService.RegisterLocalUser(e.dbCtx(t), "Mallory", otherEmail, otherPassword); err != nil {
		t.Fatal(err)
	}
	other := e.newClient()
	other.login(t, otherEmail, otherPassword)
	foreign := other.get(t, "/api/v1/secret-request/view", url.Values{"token": {raw}}).expect(t, http.StatusNotFound)
	unknown := owner.get(t, "/api/v1/secret-request/view", url.Values{"token": {"wfx_" + strings.Repeat("A", 43)}}).expect(t, http.StatusNotFound)
	if string(foreign.Body) != string(unknown.Body) {
		t.Fatalf("another org's link = %s, an unknown one = %s: must read the same", foreign.Body, unknown.Body)
	}
	other.post(t, "/api/v1/secret-request/submit", hsecretreq.SubmitRequest{Token: raw,
		Values: map[string]string{"API_KEY": cryptotest.Ciphertext(t, "a"), "DB_PASSWORD": cryptotest.Ciphertext(t, "b")}}).
		expect(t, http.StatusNotFound)

	// A plaintext, or a missing name, is refused.
	owner.post(t, "/api/v1/secret-request/submit", hsecretreq.SubmitRequest{Token: raw,
		Values: map[string]string{"API_KEY": "hunter2", "DB_PASSWORD": cryptotest.Ciphertext(t, "b")}}).expect(t, http.StatusBadRequest)
	owner.post(t, "/api/v1/secret-request/submit", hsecretreq.SubmitRequest{Token: raw,
		Values: map[string]string{"API_KEY": cryptotest.Ciphertext(t, "a")}}).expect(t, http.StatusBadRequest)

	values := hsecretreq.SubmitRequest{Token: raw,
		Values: map[string]string{"API_KEY": cryptotest.Ciphertext(t, "a"), "DB_PASSWORD": cryptotest.Ciphertext(t, "b")}}
	owner.post(t, "/api/v1/secret-request/submit", values).expect(t, http.StatusOK)
	got, err := e.deps.SecretRequests.Get(e.dbCtx(t), owner.userID, req.ID)
	if err != nil || got.Status != "submitted" {
		t.Fatalf("after the submission = %+v, %v", got, err)
	}
	// One submission per link; then the link reads as gone.
	owner.post(t, "/api/v1/secret-request/submit", values).expect(t, http.StatusNotFound)
	owner.get(t, "/api/v1/secret-request/view", url.Values{"token": {raw}}).expect(t, http.StatusNotFound)
	// The token is no credential.
	link := e.newClient()
	link.bearer = raw
	link.get(t, "/api/v1/secret-request/view", url.Values{"token": {raw}}).expect(t, http.StatusUnauthorized)
}
