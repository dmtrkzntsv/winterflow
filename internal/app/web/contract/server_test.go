package contract_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/transport/bus"
)

func TestServerRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t) // the login auto-claimed the embedded server

	t.Run("liveness follows the events queue", func(t *testing.T) {
		var st server.GetServersStatusResponse
		owner.get(t, "/api/v1/server/get-servers-status", nil).expect(t, http.StatusOK).data(t, &st)
		if len(st.Servers) != 1 || st.Servers[0].Liveness != status.LivenessUnknown {
			t.Fatalf("before heartbeat: %+v", st)
		}
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerOnline})
		eventually(t, func() bool {
			owner.get(t, "/api/v1/server/get-servers-status", nil).expect(t, http.StatusOK).data(t, &st)
			return st.Servers[0].Liveness == status.LivenessOnline
		})
	})

	t.Run("public key comes from the capability", func(t *testing.T) {
		q := url.Values{"server_id": {sid}}
		owner.get(t, "/api/v1/server/get-public-key", q).expect(t, http.StatusBadRequest)
		if err := e.deps.ServerRepository.SaveCapabilities(e.ctx, sid, map[string]string{"public_key": "BASE64POINT"}, nil); err != nil {
			t.Fatal(err)
		}
		var pk server.PublicKeyResponse
		owner.get(t, "/api/v1/server/get-public-key", q).expect(t, http.StatusOK).data(t, &pk)
		if pk.PublicKey != "BASE64POINT" {
			t.Fatalf("public key = %+v", pk)
		}
	})

	t.Run("register claims a pending server by code", func(t *testing.T) {
		code := e.seedPendingServer(t)
		owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: "ZZZZZZ"}).expect(t, http.StatusBadRequest)
		var reg server.RegisterServerResponse
		owner.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: code}).expect(t, http.StatusOK).data(t, &reg)
		var list server.GetServersResponse
		owner.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Servers) != 2 {
			t.Fatalf("servers after register = %+v", list)
		}
		// Timestamps round-trip as UTC on both databases (Postgres reads
		// naive strings in the session zone).
		for _, s := range list.Servers {
			if skew := time.Since(s.CreatedAt); skew < -time.Minute || skew > time.Minute {
				t.Fatalf("created_at %s is %s away from now", s.CreatedAt, skew)
			}
		}
	})
	// No roles: a member claims a server into the shared org, and the
	// owner sees it.
	t.Run("a member registers a server", func(t *testing.T) {
		m := owner.member(t)
		var reg server.RegisterServerResponse
		m.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: e.seedPendingServer(t)}).
			expect(t, http.StatusOK).data(t, &reg)
		var list server.GetServersResponse
		owner.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &list)
		found := false
		for _, s := range list.Servers {
			found = found || s.ID == reg.Server.ID
		}
		if !found {
			t.Fatalf("owner's servers %+v lack the member's %s", list.Servers, reg.Server.ID)
		}
	})
}
