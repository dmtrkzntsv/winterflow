package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

func TestDockerRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	q := url.Values{"server_id": {owner.serverID(t)}}

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/registry/list", nil},
		{http.MethodPost, "/api/v1/registry/create", command.CreateRegistryRequest{Address: "ghcr.io", Username: "u", Password: "p"}},
		{http.MethodPost, "/api/v1/registry/delete", command.DeleteRegistryRequest{Address: "ghcr.io"}},
		{http.MethodGet, "/api/v1/network/list", nil},
		{http.MethodPost, "/api/v1/network/create", command.CreateNetworkRequest{Name: "edge"}},
		{http.MethodPost, "/api/v1/network/delete", command.DeleteNetworkRequest{Name: "edge"}},
		{http.MethodGet, "/api/v1/volume/list", nil},
		{http.MethodPost, "/api/v1/volume/create", command.CreateVolumeRequest{Name: "data"}},
		{http.MethodPost, "/api/v1/volume/delete", command.DeleteVolumeRequest{Name: "data"}},
		{http.MethodPost, "/api/v1/agent/update", command.UpdateAgentRequest{Version: "v261010.1"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			owner.succeed(t, call{Method: tc.method, Path: tc.path, Query: q, Body: tc.body}, nil)
		})
	}

	// No roles: a member runs every docker route the owner does, even one
	// whose stored role is still "member" from before the upgrade.
	t.Run("members mutate infrastructure", func(t *testing.T) {
		m := owner.member(t)
		e.setStoredRole(t, m.userID, "member")
		for _, tc := range cases {
			m.succeed(t, call{Method: tc.method, Path: tc.path, Query: q, Body: tc.body}, nil)
		}
		// Org ownership still holds: another org's server stays 403.
		n := e.agent.count()
		m.do(t, call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: sq(e.foreignServer(t)),
			Body: command.CreateNetworkRequest{Name: "x"}}).expect(t, http.StatusForbidden)
		if e.agent.count() != n {
			t.Fatal("a command for a foreign server reached the agent")
		}
	})

	t.Run("agent errors", func(t *testing.T) {
		e.agent.fail(command.TypeRegistryCreate, "login failed")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/registry/create", Query: q,
			Body: command.CreateRegistryRequest{Address: "ghcr.io", Username: "u", Password: "p"}}, "login failed")
		e.agent.fail(command.TypeNetworkList, "docker down")
		owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/network/list", Query: q}, "docker down")
		e.agent.fail(command.TypeVolumeDelete, "volume in use")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/volume/delete", Query: q,
			Body: command.DeleteVolumeRequest{Name: "data"}}, "volume in use")
		e.agent.fail(command.TypeAgentUpdate, "no such release")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/agent/update", Query: q,
			Body: command.UpdateAgentRequest{Version: "v0"}}, "no such release")
	})
}
