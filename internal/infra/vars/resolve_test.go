package vars

import (
	"errors"
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
)

// fakeDecrypt "decrypts" ENC:<x> to <x> and fails on anything else.
func fakeDecrypt(ct string) (string, error) {
	if len(ct) > 4 && ct[:4] == "ENC:" {
		return ct[4:], nil
	}
	return "", errors.New("bad ciphertext")
}

func TestResolvePrecedenceAndShadowing(t *testing.T) {
	snap := command.SyncVarsRequest{
		Org: []command.SharedVar{
			{Name: "SMTP_HOST", Value: "org-mail"},
			{Name: "SMTP_PASS", IsSecret: true, Ciphertext: "ENC:org-pass"},
			{Name: "BASE", Value: "org.example"},
			{Name: "TOKEN", Value: "org-plain"},
		},
		Server: []command.SharedVar{
			{Name: "SMTP_HOST", Value: "local-mail"},                      // server beats org
			{Name: "BASE", IsSecret: true, Ciphertext: "ENC:secret-base"}, // a server secret shadows an org plain value
			{Name: "SMTP_PASS", Value: "plain-pass"},                      // and a server plain shadows an org secret
		},
	}
	res := Resolve(snap, map[string]string{"WF_SERVER_ID": "s1"}, fakeDecrypt)
	want := map[string]Value{
		"WF_SERVER_ID": {Value: "s1", Scope: ScopeBuiltin},
		"SMTP_HOST":    {Value: "local-mail", Scope: ScopeServer},
		"SMTP_PASS":    {Value: "plain-pass", Scope: ScopeServer},
		"BASE":         {Value: "secret-base", Secret: true, Scope: ScopeServer},
		"TOKEN":        {Value: "org-plain", Scope: ScopeOrg},
	}
	if !reflect.DeepEqual(res.Values, want) {
		t.Fatalf("values = %+v", res.Values)
	}
	if len(res.Undecryptable) != 0 || len(res.Missing) != 0 {
		t.Fatalf("unexpected problems: %+v", res)
	}
}

func TestResolveSkipsAndReportsBrokenSecrets(t *testing.T) {
	snap := command.SyncVarsRequest{
		Org: []command.SharedVar{
			{Name: "KEY", Value: "org-fallback"},
			{Name: "LOST", IsSecret: true},                       // no copy for this server
			{Name: "BROKEN", IsSecret: true, Ciphertext: "junk"}, // wrong key
		},
		Server: []command.SharedVar{{Name: "KEY", IsSecret: true, Ciphertext: "junk"}},
	}
	res := Resolve(snap, nil, fakeDecrypt)
	if v := res.Values["KEY"]; v.Value != "org-fallback" || v.Scope != ScopeOrg {
		t.Fatalf("a broken server secret must fall back to the org value, got %+v", v)
	}
	if _, ok := res.Values["LOST"]; ok {
		t.Fatal("missing secret resolved")
	}
	if _, ok := res.Values["BROKEN"]; ok {
		t.Fatal("undecryptable secret resolved")
	}
	wantUndec := []command.VarRef{{Scope: ScopeOrg, Name: "BROKEN"}, {Scope: ScopeServer, Name: "KEY"}}
	if !reflect.DeepEqual(res.Undecryptable, wantUndec) {
		t.Fatalf("undecryptable = %+v", res.Undecryptable)
	}
	if !reflect.DeepEqual(res.Missing, []command.VarRef{{Scope: ScopeOrg, Name: "LOST"}}) {
		t.Fatalf("missing = %+v", res.Missing)
	}
}

func TestBuiltins(t *testing.T) {
	srv := ServerBuiltins(command.SyncVarsRequest{ServerID: "s1", ServerName: "box", OrgID: "o1"}, "/opt/wf", "Europe/Berlin")
	want := map[string]string{"WF_SERVER_ID": "s1", "WF_SERVER_NAME": "box", "WF_ORG_ID": "o1", "WF_DATA_DIR": "/opt/wf", "WF_TZ": "Europe/Berlin"}
	if !reflect.DeepEqual(srv, want) {
		t.Fatalf("server builtins = %v", srv)
	}
	app := AppBuiltins("a1", "nextcloud", "/opt/wf/apps-data/a1")
	if !reflect.DeepEqual(app, map[string]string{"WF_APP_ID": "a1", "WF_APP_SLUG": "nextcloud", "WF_APP_DIR": "/opt/wf/apps-data/a1"}) {
		t.Fatalf("app builtins = %v", app)
	}
}
