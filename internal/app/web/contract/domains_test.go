package contract_test

import (
	"net/http"
	"net/url"
	"testing"

	"winterflow/internal/domain/command"
)

// indexed is the API's server_domains index for serverID, as names.
func (e *env) indexed(t *testing.T, serverID string) map[string]string {
	t.Helper()
	rows, err := e.deps.ServerDomainRepository.ListForServer(e.ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.TLS
	}
	return out
}

// registerDomain registers name on serverID through the API.
func registerDomain(t *testing.T, c *client, serverID, name string, mode command.DomainTLS) {
	t.Helper()
	c.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: sq(serverID),
		Body: command.CreateDomainRequest{Name: name, TLS: mode}}, nil)
}

func TestDomainRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	q := url.Values{"server_id": {sid}}

	t.Run("create enters the index", func(t *testing.T) {
		var created command.CreateDomainResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "*.example.test", TLS: command.DomainTLSOff}}, &created)
		if created.Domain.Name != "*.example.test" || created.Domain.TLS != command.DomainTLSOff {
			t.Fatalf("created = %+v", created)
		}
		// Certificate mode carries the PEM chain and the encrypted key.
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "secure.test", TLS: command.DomainTLSCertificate,
				Certificate: "-----BEGIN CERTIFICATE-----", Key: "BASE64CIPHERTEXT"}}, nil)
		got := e.agent.seen(command.TypeDomainCreate)
		if in := decode[command.CreateDomainRequest](got[len(got)-1].Payload); in.Key != "BASE64CIPHERTEXT" {
			t.Fatalf("the key reached the agent as %q", in.Key)
		}
		if idx := e.indexed(t, sid); idx["*.example.test"] != "off" || idx["secure.test"] != "certificate" {
			t.Fatalf("index = %v", idx)
		}
	})
	t.Run("list reports the agent's registry and reconciles", func(t *testing.T) {
		// Registered on the agent behind the API's back (upgrade seeding).
		e.agent.register(sid, command.Domain{Name: "legacy.test", TLS: command.DomainTLSAuto, Apps: []string{"a1"}})
		var listed command.ListDomainsResponse
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/domain/list", Query: q}, &listed)
		if len(listed.Domains) != 3 || listed.Domains[1].Name != "legacy.test" || listed.Domains[1].Apps[0] != "a1" {
			t.Fatalf("listed = %+v", listed)
		}
		if idx := e.indexed(t, sid); len(idx) != 3 || idx["legacy.test"] != "auto" {
			t.Fatalf("index = %v", idx)
		}
	})
	t.Run("refresh-apps also reconciles domains", func(t *testing.T) {
		e.agent.register(sid, command.Domain{Name: "seeded.test", TLS: command.DomainTLSOff})
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: q}, nil)
		eventually(t, func() bool { return e.indexed(t, sid)["seeded.test"] == "off" })
	})
	t.Run("delete leaves the index", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: q,
			Body: command.DeleteDomainRequest{Name: "secure.test"}}, nil)
		if _, ok := e.indexed(t, sid)["secure.test"]; ok {
			t.Fatal("deleted registration still indexed")
		}
	})
	t.Run("a refused delete keeps the registration", func(t *testing.T) {
		e.agent.fail(command.TypeDomainDelete, "*.example.test is used by Shop (a1)")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: q,
			Body: command.DeleteDomainRequest{Name: "*.example.test"}}, "*.example.test is used by Shop (a1)")
		if _, ok := e.indexed(t, sid)["*.example.test"]; !ok {
			t.Fatal("a refused delete left the index")
		}
	})
	t.Run("agent errors", func(t *testing.T) {
		e.agent.fail(command.TypeDomainCreate, "the certificate does not cover x.test (it covers y.test)")
		owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "x.test", TLS: command.DomainTLSCertificate, Certificate: "C", Key: "K"}},
			"the certificate does not cover x.test (it covers y.test)")
		e.agent.fail(command.TypeDomainList, "ingress is not available on this agent")
		owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/domain/list", Query: q}, "ingress is not available on this agent")
		if _, ok := e.indexed(t, sid)["x.test"]; ok {
			t.Fatal("a refused create entered the index")
		}
	})
	t.Run("the API rejects a bad name before the agent", func(t *testing.T) {
		n := e.agent.count()
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: q,
			Body: command.CreateDomainRequest{Name: "*.*.example.test", TLS: command.DomainTLSOff}}).expect(t, http.StatusBadRequest)
		if e.agent.count() != n {
			t.Fatal("an invalid registration reached the agent")
		}
	})
}
