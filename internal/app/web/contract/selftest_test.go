package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

func TestHealth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.anon().get(t, "/_/health", nil).expect(t, http.StatusOK)
}

// TestHarnessRoundTrip proves the harness end to end: claim + auto-claimed
// server, an agent-bound call answered by the fake agent over SSE, a
// scripted agent error, PAT auth, 401 and the foreign-org 403.
func TestHarnessRoundTrip(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	q := url.Values{"server_id": {sid}}

	var list command.ListNetworksResponse
	owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, &list)
	if len(list.Networks) != 1 || len(e.agent.seen(command.TypeNetworkList)) != 1 {
		t.Fatalf("networks = %+v", list)
	}
	e.agent.fail(command.TypeNetworkList, "docker down")
	owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, "docker down")

	owner.pat(t).get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK)
	e.anon().get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusUnauthorized)
	owner.get(t, "/api/v1/server/get-public-key", url.Values{"server_id": {e.foreignServer(t)}}).
		expect(t, http.StatusForbidden)
}
