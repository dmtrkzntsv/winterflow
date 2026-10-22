package contract_test

import (
	"crypto/elliptic"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/enroll"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
)

func TestEnrollRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	agent := e.anon() // the agent has no account
	owner := e.claim(t)

	var started enroll.CodeResponse
	agent.post(t, "/api/v1/agent/enroll", enroll.StartRequest{CSR: enrolltest.CSR(t, elliptic.P256()), Hostname: "box"}).
		expect(t, http.StatusOK).data(t, &started)
	agent.post(t, "/api/v1/agent/enroll", enroll.StartRequest{CSR: "garbage"}).expect(t, http.StatusBadRequest)

	var refreshed enroll.CodeResponse
	agent.post(t, "/api/v1/agent/enroll/refresh", enroll.PollRequest{PollSecret: started.PollSecret}).
		expect(t, http.StatusOK).data(t, &refreshed)
	if refreshed.Code == started.Code || refreshed.CodesLeft != started.CodesLeft-1 {
		t.Fatalf("refresh: %+v after %+v", refreshed, started)
	}

	var st enroll.StatusResponse
	agent.post(t, "/api/v1/agent/enroll/status", enroll.PollRequest{PollSecret: started.PollSecret}).
		expect(t, http.StatusOK).data(t, &st)
	if st.State != "pending" || st.Code != refreshed.Code {
		t.Fatalf("pending status: %+v", st)
	}
	agent.post(t, "/api/v1/agent/enroll/status", enroll.PollRequest{PollSecret: "unknown"}).expect(t, http.StatusBadRequest)

	// The user claims the code. Nothing is trusted yet: the claimed answer
	// carries an empty set.
	var reg server.RegisterServerResponse
	owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: refreshed.Code}).
		expect(t, http.StatusOK).data(t, &reg)
	var raw map[string]json.RawMessage
	agent.post(t, "/api/v1/agent/enroll/status", enroll.PollRequest{PollSecret: started.PollSecret}).
		expect(t, http.StatusOK).data(t, &raw)
	if got := string(raw["hub_fingerprints"]); got != "[]" {
		t.Fatalf("hub_fingerprints = %q, want []", got)
	}
	// The control plane's hub certificate is trusted; claimed answers carry
	// the set.
	const pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := e.deps.HubCertRepository.Trust(e.dbCtx(t), enrolltest.DefaultHub.ID, pin, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The agent's poll returns its certificate.
	agent.post(t, "/api/v1/agent/enroll/status", enroll.PollRequest{PollSecret: started.PollSecret}).
		expect(t, http.StatusOK).data(t, &st)
	if st.State != "claimed" || st.ServerID != reg.Server.ID || st.Certificate == "" || st.CACertificate == "" || st.HubAddr != enrolltest.DefaultHub.PublicAddr ||
		st.HubFingerprints == nil || len(*st.HubFingerprints) != 1 || (*st.HubFingerprints)[0] != pin {
		t.Fatalf("claimed status: %+v", st)
	}
}
