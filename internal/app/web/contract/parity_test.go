package contract_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	hrollouts "winterflow/internal/app/web/handler/rollouts"
	"winterflow/internal/app/web/handler/server"
	hsv "winterflow/internal/app/web/handler/sharedvars"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/app/web/handler/user"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/sharedvars"
	tuc "winterflow/internal/domain/usecase/templates"
)

var (
	goldenPath = filepath.Join("testdata", "parity.golden.json")
	update     = flag.Bool("update", false, "rewrite testdata/parity.golden.json")
)

// step is one transcript entry: what was called and what came back.
type step struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	Body   any    `json:"body"`
}

var (
	uuidRE  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timeRE  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	tokenRE = regexp.MustCompile(`wfp_[A-Za-z0-9_-]+`)
)

// normalizer replaces volatile values with stable placeholders: the n-th
// distinct UUID becomes <id-n>, every timestamp <time> followed by its zone
// (so a Z on one database and -04:00 on the other still differ), every PAT
// <token>.
// Re-decoding into any and re-encoding sorts object keys.
type normalizer struct{ ids map[string]string }

func (n *normalizer) body(t *testing.T, raw []byte) any {
	t.Helper()
	s := tokenRE.ReplaceAllString(string(raw), "<token>")
	s = timeRE.ReplaceAllString(s, "<time>${2}")
	s = uuidRE.ReplaceAllStringFunc(s, func(id string) string {
		if p, ok := n.ids[id]; ok {
			return p
		}
		p := fmt.Sprintf("<id-%d>", len(n.ids)+1)
		n.ids[id] = p
		return p
	})
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("normalize %s: %v", raw, err)
	}
	return v
}

func TestNormalizerKeepsTimeZone(t *testing.T) {
	t.Parallel()
	n := &normalizer{ids: map[string]string{}}
	got := n.body(t, []byte(`["2026-10-10T12:00:00Z","2026-10-10T08:00:00.123-04:00"]`))
	if want := []any{"<time>Z", "<time>-04:00"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}
}

// TestParity runs one fixed scenario and compares the normalized transcript
// with testdata/parity.golden.json. Both databases must produce the same
// file; -update rewrites it (run it on SQLite, then confirm on Postgres).
func TestParity(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	n := &normalizer{ids: map[string]string{}}
	var steps []step
	rec := func(name string, r *response) *response {
		steps = append(steps, step{Name: name, Status: r.Status, Body: n.body(t, r.Body)})
		return r
	}
	result := func(name string, c *client, k call) json.RawMessage {
		ev := awaitResult(t, c, c.accepted(t, k))
		steps = append(steps, step{Name: name + " (result)", Status: int(ev.Status), Body: n.body(t, ev.Payload)})
		return ev.Payload
	}

	owner := e.claim(t)
	var tok user.CreateTokenResponse
	rec("create token", owner.post(t, "/api/v1/user/create-token", user.CreateTokenRequest{Name: "parity"})).
		expect(t, http.StatusOK).data(t, &tok)
	p := e.bearer(t, tok.Token, owner)
	code := e.seedPendingServer(t)
	var reg server.RegisterServerResponse
	rec("register server", p.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: code})).
		expect(t, http.StatusOK).data(t, &reg)
	sid := reg.Server.ID

	var ids []string
	for _, name := range []string{"alpha", "beta"} {
		raw := result("save "+name, p, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: name, Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
		}})
		var saved command.SaveAppResponse
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatalf("save %s result: %v", name, err)
		}
		ids = append(ids, saved.AppID)
	}
	q := url.Values{"server_id": {sid}}
	rec("list apps", p.get(t, "/api/v1/app/get-apps", q)).expect(t, http.StatusOK)
	result("rename alpha", p, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
		Body: happ.RenameAppRequest{ServerID: sid, AppID: ids[0], Name: "alpha-2"}})
	result("delete beta", p, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
		Body: happ.DeleteAppRequest{ServerID: sid, AppID: ids[1]}})
	rec("list apps again", p.get(t, "/api/v1/app/get-apps", q)).expect(t, http.StatusOK)
	result("save storage", p, call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: q,
		Body: command.SaveStorageRequest{Storage: model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb/"}}, Create: true}})
	rec("list storages", p.get(t, "/api/v1/storage/get-storages", q)).expect(t, http.StatusOK)
	rec("list members", p.get(t, "/api/v1/org/get-members", nil)).expect(t, http.StatusOK)
	var saved sharedvars.VariableView
	rec("save org variable", p.post(t, "/api/v1/org/save-shared-variable",
		hsv.SaveVariableRequest{Name: "PARITY", Value: strp("1"), Description: "parity"})).
		expect(t, http.StatusOK).data(t, &saved)

	var tpl tuc.SavedTemplate
	rec("save template", p.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{
		Name: "parity", Body: template.Body{Files: map[string]string{"compose.yaml": "services: {app: {image: x}}\n"}, Summary: "Parity"},
	})).expect(t, http.StatusOK).data(t, &tpl)
	rec("read template", p.get(t, "/api/v1/template/get-template", url.Values{"id": {tpl.ID}})).expect(t, http.StatusOK)
	rec("create instance (wait)", p.post(t, "/api/v1/instance/create-instance", htpl.CreateInstanceRequest{
		ServerID: sid, Key: "parity-1", TemplateID: tpl.ID, Wait: true,
	})).expect(t, http.StatusOK)
	rec("list templates", p.get(t, "/api/v1/template/get-templates", nil)).expect(t, http.StatusOK)
	// The only instance is on the latest revision: the upgrade ends at once.
	rec("upgrade all instances", p.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: tpl.ID})).
		expect(t, http.StatusOK)
	rq := url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {tpl.ID}}
	eventually(t, func() bool {
		var list hrollouts.RolloutList
		p.get(t, "/api/v1/rollout/get-rollouts", rq).expect(t, http.StatusOK).data(t, &list)
		return len(list.Rollouts) == 1 && list.Rollouts[0].State == string(model.RolloutDone)
	})
	rec("list rollouts", p.get(t, "/api/v1/rollout/get-rollouts", rq)).expect(t, http.StatusOK)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep "<id-1>" readable in the golden file
	enc.SetIndent("", "  ")
	if err := enc.Encode(steps); err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./internal/app/web/contract -run TestParity -update)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s transcript differs from %s (dialect %s):\n%s", dialect(t), goldenPath, dialect(t), lineDiff(string(want), string(got)))
	}
}
