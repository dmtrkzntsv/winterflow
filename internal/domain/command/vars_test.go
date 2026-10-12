package command

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSyncVarsRequestValidate(t *testing.T) {
	ok := SyncVarsRequest{Revision: 3, ServerID: "s", Org: []SharedVar{
		{Name: "SMTP_HOST", Value: "mail"},
		{Name: "SMTP_PASS", IsSecret: true, Ciphertext: "CT"},
		{Name: "MISSING", IsSecret: true}, // no copy for this server yet
	}, Server: []SharedVar{{Name: "SMTP_HOST", Value: "local"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	bad := map[string]SyncVarsRequest{
		"negative revision": {Revision: -1},
		"reserved name":     {Org: []SharedVar{{Name: "WF_X", Value: "1"}}},
		"bad name":          {Server: []SharedVar{{Name: "1X", Value: "1"}}},
		"duplicate":         {Org: []SharedVar{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}}},
		"secret with value": {Org: []SharedVar{{Name: "A", IsSecret: true, Value: "plain"}}},
		"plain with ct":     {Org: []SharedVar{{Name: "A", Ciphertext: "CT"}}},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestReloadVarsRequestValidate(t *testing.T) {
	if err := (ReloadVarsRequest{AppIDs: []string{"a1"}, Names: []string{"SMTP_PASS"}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ReloadVarsRequest{AppIDs: []string{"../x"}}).Validate(); err == nil {
		t.Fatal("path-like app id accepted")
	}
	if err := (ReloadVarsRequest{Names: []string{"bad name"}}).Validate(); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestVarsWireShapes(t *testing.T) {
	raw, _ := json.Marshal(SyncVarsRequest{Revision: 2, ServerID: "s", ServerName: "box", OrgID: "o",
		Org: []SharedVar{{Name: "A", Value: "1"}}, Server: []SharedVar{{Name: "B", IsSecret: true, Ciphertext: "CT"}}})
	for _, want := range []string{`"revision":2`, `"server_name":"box"`, `"org_id":"o"`, `"org":[{"name":"A","is_secret":false,"value":"1"}]`, `"ciphertext":"CT"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("sync request %s lacks %s", raw, want)
		}
	}
	raw, _ = json.Marshal(GetAppsStatusResponse{Apps: []AppStatus{{AppID: "a", StaleVars: []string{"X"}}}})
	if !strings.Contains(string(raw), `"stale_vars":["X"]`) || strings.Contains(string(raw), "vars_revision") {
		t.Errorf("status without revision = %s", raw)
	}
	rev := int64(0)
	raw, _ = json.Marshal(GetAppsStatusResponse{Apps: []AppStatus{}, VarsRevision: &rev})
	if !strings.Contains(string(raw), `"vars_revision":0`) {
		t.Errorf("a zero revision must still be sent: %s", raw)
	}
}
