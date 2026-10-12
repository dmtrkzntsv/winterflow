package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	hsv "winterflow/internal/app/web/handler/sharedvars"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/transport/bus"
)

func strp(s string) *string { return &s }

// setKey records a server's ECIES public key as its agent's registration would.
func setKey(t *testing.T, e *env, serverID, key string) {
	t.Helper()
	if err := e.deps.ServerRepository.SaveCapabilities(e.dbCtx(t), serverID, map[string]string{"public_key": key}, nil); err != nil {
		t.Fatal(err)
	}
}

// syncsTo is every vars.sync snapshot the fake agent received for serverID.
func syncsTo(e *env, serverID string) []command.SyncVarsRequest {
	var out []command.SyncVarsRequest
	for _, c := range e.agent.seen(command.TypeVarsSync) {
		if c.AgentID == serverID {
			out = append(out, decode[command.SyncVarsRequest](c.Payload))
		}
	}
	return out
}

func orgVars(t *testing.T, c *client) (sharedvars.OrgVariables, string) {
	t.Helper()
	var out sharedvars.OrgVariables
	r := c.get(t, "/api/v1/org/list-shared-variables", nil).expect(t, http.StatusOK)
	r.data(t, &out)
	return out, string(r.Body)
}

func findVar(list []sharedvars.VariableView, name string) sharedvars.VariableView {
	for _, v := range list {
		if v.Name == name {
			return v
		}
	}
	return sharedvars.VariableView{}
}

// noSecrets fails when a response body carries a ciphertext (every test
// ciphertext starts with "CT-"); the API never sees a secret's plaintext.
func noSecrets(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "CT-") {
		t.Fatalf("a response carries a ciphertext: %s", body)
	}
}

func TestSharedVariableRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	owner.subscribe(t)
	sidA := owner.serverID(t)
	setKey(t, e, sidA, "KEY-A")
	sidB := owner.registerServer(t)
	setKey(t, e, sidB, "KEY-B")
	sidP := owner.registerServer(t) // never reported a key: pending
	foreign := e.foreignServer(t)
	save := func(t *testing.T, body hsv.SaveVariableRequest) *response {
		t.Helper()
		r := owner.post(t, "/api/v1/org/save-shared-variable", body)
		noSecrets(t, string(r.Body))
		return r
	}

	t.Run("encryption keys", func(t *testing.T) {
		var keys sharedvars.EncryptionKeys
		owner.get(t, "/api/v1/org/list-encryption-keys", nil).expect(t, http.StatusOK).data(t, &keys)
		if len(keys.Servers) != 2 || len(keys.PendingServers) != 1 || keys.PendingServers[0].ServerID != sidP {
			t.Fatalf("keys = %+v", keys)
		}
	})

	var hostID string
	t.Run("a plain org variable is pushed to every org server", func(t *testing.T) {
		var v sharedvars.VariableView
		save(t, hsv.SaveVariableRequest{Name: "SMTP_HOST", Value: strp("mail.example"), Description: "relay"}).
			expect(t, http.StatusOK).data(t, &v)
		hostID = v.ID
		eventually(t, func() bool {
			return len(syncsTo(e, sidA)) > 0 && len(syncsTo(e, sidB)) > 0 && len(syncsTo(e, sidP)) > 0
		})
		got := syncsTo(e, sidA)
		if last := got[len(got)-1]; len(last.Org) != 1 || last.Org[0].Value != "mail.example" || last.ServerID != sidA {
			t.Fatalf("snapshot = %+v", last)
		}
		if len(syncsTo(e, foreign)) != 0 {
			t.Fatal("another org's server got the snapshot")
		}
	})

	t.Run("a plain variable needs a value", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "NO_VALUE"}).expect(t, http.StatusBadRequest)
	})

	t.Run("a secret never travels as value", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Value: strp(command.EncryptedPlaceholder),
			Ciphertexts: map[string]string{sidA: "CT-A", sidB: "CT-B"}}).expect(t, http.StatusBadRequest)
	})

	t.Run("a new secret must cover every keyed server", func(t *testing.T) {
		r := save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-A"}}).
			expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), sidB) {
			t.Fatalf("400 does not name the uncovered server: %s", r.Body)
		}
	})

	t.Run("a ciphertext for another org's server is 403", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true,
			Ciphertexts: map[string]string{sidA: "CT-A", sidB: "CT-B", foreign: "CT-X"}}).expect(t, http.StatusForbidden)
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: sidA, Name: "OWN", IsSecret: true,
			Ciphertexts: map[string]string{foreign: "CT-X"}}).expect(t, http.StatusForbidden)
	})

	var passID string
	t.Run("a covered secret is stored and never returned", func(t *testing.T) {
		var v sharedvars.VariableView
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-A", sidB: "CT-B"}}).
			expect(t, http.StatusOK).data(t, &v)
		passID = v.ID
		list, body := orgVars(t, owner)
		noSecrets(t, body)
		if p := findVar(list.Variables, "SMTP_PASS"); p.Value != command.EncryptedPlaceholder || len(p.MissingOn) != 0 {
			t.Fatalf("listed = %+v", p)
		}
	})

	t.Run("a gap fill is accepted, an overwriting partial map is not", func(t *testing.T) {
		setKey(t, e, sidP, "KEY-P")
		list, _ := orgVars(t, owner)
		if p := findVar(list.Variables, "SMTP_PASS"); len(p.MissingOn) != 1 || p.MissingOn[0] != sidP {
			t.Fatalf("missing = %+v", p.MissingOn)
		}
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-ROT"}}).
			expect(t, http.StatusBadRequest)
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidP: "CT-P"}}).
			expect(t, http.StatusOK)
		list, _ = orgVars(t, owner)
		if p := findVar(list.Variables, "SMTP_PASS"); len(p.MissingOn) != 0 {
			t.Fatalf("still missing on %v", p.MissingOn)
		}
		cts, err := e.deps.SharedVariableRepository.CiphertextsForServer(e.dbCtx(t), sidA)
		if err != nil || cts[passID] != "CT-A" {
			t.Fatalf("the gap fill touched alpha's copy: %v %v", cts, err)
		}
	})

	var localID string
	t.Run("a server variable overrides the org one", func(t *testing.T) {
		var v sharedvars.VariableView
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: sidA, Name: "SMTP_HOST", Value: strp("local")}).
			expect(t, http.StatusOK).data(t, &v)
		localID = v.ID
		var ls sharedvars.ServerVariables
		r := owner.get(t, "/api/v1/server/list-shared-variables", url.Values{"server_id": {sidA}}).expect(t, http.StatusOK)
		r.data(t, &ls)
		noSecrets(t, string(r.Body))
		if len(ls.Variables) != 1 || ls.Variables[0].Value != "local" {
			t.Fatalf("own = %+v", ls.Variables)
		}
		overridden := map[string]bool{}
		for _, iv := range ls.Inherited {
			overridden[iv.Name] = iv.Overridden
		}
		if !overridden["SMTP_HOST"] || overridden["SMTP_PASS"] || len(ls.BuiltIns) != 8 {
			t.Fatalf("server listing = %+v", ls)
		}
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: sidA, Name: "OWN", IsSecret: true,
			Ciphertexts: map[string]string{sidB: "CT-B"}}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: foreign, Name: "OWN", Value: strp("x")}).
			expect(t, http.StatusForbidden)
		owner.get(t, "/api/v1/server/list-shared-variables", url.Values{"server_id": {foreign}}).expect(t, http.StatusForbidden)
	})

	t.Run("reserved names and kind changes are refused", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "WF_SERVER_ID", Value: strp("x")}).expect(t, http.StatusBadRequest)
		save(t, hsv.SaveVariableRequest{Name: "COMPOSE_PROJECT_NAME", Value: strp("x")}).expect(t, http.StatusBadRequest)
		save(t, hsv.SaveVariableRequest{Name: "SMTP_PASS", Value: strp("now plain")}).expect(t, http.StatusBadRequest)
		save(t, hsv.SaveVariableRequest{Name: strings.Repeat("A", 300), Value: strp("x")}).expect(t, http.StatusBadRequest)
	})

	t.Run("vars.synced reaches the org", func(t *testing.T) {
		awaitEvent(t, owner, "vars.synced", func(ev event) bool { return ev.Type == model.NotificationVarsSynced })
	})

	t.Run("undecryptable copies are dropped", func(t *testing.T) {
		e.agent.on(command.TypeVarsSync, func(raw json.RawMessage) reply {
			in := decode[command.SyncVarsRequest](raw)
			und := []command.VarRef{}
			if in.ServerID == sidB {
				und = append(und, command.VarRef{Scope: "org", Name: "SMTP_PASS"})
			}
			return reply{Payload: command.SyncVarsResponse{Revision: in.Revision, Undecryptable: und}}
		})
		defer e.agent.on(command.TypeVarsSync, defaultScript(command.TypeVarsSync))
		var d sharedvars.DispatchedServers
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{ServerID: sidB}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 1 || d.Servers[0] != sidB {
			t.Fatalf("resync = %+v", d)
		}
		eventually(t, func() bool {
			list, _ := orgVars(t, owner)
			p := findVar(list.Variables, "SMTP_PASS")
			return len(p.MissingOn) == 1 && p.MissingOn[0] == sidB
		})
	})

	t.Run("a changed server key purges its copies", func(t *testing.T) {
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventCapabilities, Payload: []byte(`{"capabilities":{"public_key":"KEY-A-NEW"}}`)})
		eventually(t, func() bool {
			list, _ := orgVars(t, owner)
			for _, id := range findVar(list.Variables, "SMTP_PASS").MissingOn {
				if id == sidA {
					return true
				}
			}
			return false
		})
	})

	t.Run("an agent behind is pushed once per revision", func(t *testing.T) {
		eventually(t, func() bool { return len(syncsTo(e, sidA)) > 0 }) // the reconnect sync above
		time.Sleep(100 * time.Millisecond)
		n := len(syncsTo(e, sidA))
		rev := int64(0)
		status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{}, VarsRevision: &rev})
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventAppsStatus, Payload: status})
		eventually(t, func() bool { return len(syncsTo(e, sidA)) == n+1 })
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventAppsStatus, Payload: status})
		time.Sleep(200 * time.Millisecond)
		if got := len(syncsTo(e, sidA)); got != n+1 {
			t.Fatalf("pushes = %d, want %d", got, n+1)
		}
	})

	t.Run("resync and reload", func(t *testing.T) {
		var d sharedvars.DispatchedServers
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 3 {
			t.Fatalf("resync all = %+v", d)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sidA, AppIDs: []string{anApp}}).
			expect(t, http.StatusOK).data(t, &d)
		eventually(t, func() bool {
			for _, c := range e.agent.seen(command.TypeVarsReload) {
				if in := decode[command.ReloadVarsRequest](c.Payload); c.AgentID == sidA && len(in.AppIDs) == 1 && in.AppIDs[0] == anApp {
					return true
				}
			}
			return false
		})
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{VariableID: hostID}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 3 {
			t.Fatalf("reload by org variable = %+v", d)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: foreign}).expect(t, http.StatusForbidden)
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{ServerID: foreign}).expect(t, http.StatusForbidden)
	})

	t.Run("reload: true follows each sync with vars.reload", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "RELOAD_ME", Value: strp("1"), Reload: true}).expect(t, http.StatusOK)
		eventually(t, func() bool {
			n := 0
			for _, c := range e.agent.seen(command.TypeVarsReload) {
				if in := decode[command.ReloadVarsRequest](c.Payload); len(in.Names) == 1 && in.Names[0] == "RELOAD_ME" {
					n++
				}
			}
			return n == 3
		})
	})

	t.Run("vars.reloaded from the agent fans out", func(t *testing.T) {
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventVarsReloaded,
			Payload: []byte(`{"app_id":"` + anApp + `","ok":true,"names":["SMTP_HOST"]}`)})
		awaitEvent(t, owner, "vars.reloaded", func(ev event) bool {
			return ev.Type == model.NotificationVarsReloaded && strings.Contains(string(ev.Payload), sidA)
		})
	})

	t.Run("delete", func(t *testing.T) {
		owner.post(t, "/api/v1/server/delete-shared-variable", hsv.DeleteServerVariableRequest{ServerID: foreign, ID: localID}).
			expect(t, http.StatusForbidden)
		owner.post(t, "/api/v1/server/delete-shared-variable", hsv.DeleteServerVariableRequest{ServerID: sidA, ID: localID}).
			expect(t, http.StatusOK)
		owner.post(t, "/api/v1/org/delete-shared-variable", hsv.DeleteVariableRequest{ID: hostID}).expect(t, http.StatusOK)
		owner.post(t, "/api/v1/org/delete-shared-variable", hsv.DeleteVariableRequest{ID: hostID}).expect(t, http.StatusBadRequest)
		if list, _ := orgVars(t, owner); findVar(list.Variables, "SMTP_HOST").ID != "" {
			t.Fatal("deleted variable still listed")
		}
	})

	t.Run("another org sees none of it", func(t *testing.T) {
		m := e.newClient()
		m.login(t, "mallory@other.test", "password")
		list, _ := orgVars(t, m)
		if len(list.Variables) != 0 || len(list.Servers) != 1 || list.Servers[0].ServerID != foreign {
			t.Fatalf("mallory sees %+v", list)
		}
		m.post(t, "/api/v1/org/delete-shared-variable", hsv.DeleteVariableRequest{ID: passID}).expect(t, http.StatusBadRequest)
	})
}
