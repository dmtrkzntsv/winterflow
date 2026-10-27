package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/api/openapi"
	happ "winterflow/internal/app/web/handler/app"
	hauth "winterflow/internal/app/web/handler/auth"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// specPath is the committed document, relative to this package.
var specPath = filepath.Join("..", "..", "..", "api", "openapi.yaml")

func TestOpenAPIUpToDate(t *testing.T) {
	want, err := openapi.YAML(APIRoutes())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v (run make openapi)", specPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("api/openapi.yaml is stale: run make openapi and commit the result\n%s",
			lineDiff(string(got), string(want), 40))
	}
}

// lineDiff renders a zero-context unified diff of a (committed) against b
// (generated), stopping after max changed lines. After a mismatch it
// resyncs at the nearest line the two sides share again: good enough for
// a test failure message, without a diff dependency.
func lineDiff(a, b string, max int) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	out := []string{"--- api/openapi.yaml (committed)", "+++ api/openapi.yaml (generated)"}
	changed := 0
	for i, j := 0, 0; i < len(al) || j < len(bl); {
		if i < len(al) && j < len(bl) && al[i] == bl[j] {
			i, j = i+1, j+1
			continue
		}
		if changed >= max {
			out = append(out, "... more differences")
			break
		}
		di, dj := resync(al[i:], bl[j:])
		out = append(out, fmt.Sprintf("@@ -%d,%d +%d,%d @@", i+1, di, j+1, dj))
		hunk := make([]string, 0, di+dj)
		for _, l := range al[i : i+di] {
			hunk = append(hunk, "-"+l)
		}
		for _, l := range bl[j : j+dj] {
			hunk = append(hunk, "+"+l)
		}
		if room := max - changed; len(hunk) > room {
			out = append(append(out, hunk[:room]...), "... more differences")
			break
		}
		out = append(out, hunk...)
		changed += len(hunk)
		i, j = i+di, j+dj
	}
	return strings.Join(out, "\n")
}

// resync returns how many lines of a and b to skip so they start equal
// again, preferring the smallest total skip.
func resync(a, b []string) (int, int) {
	for d := 1; d < len(a)+len(b); d++ {
		for k := 0; k <= d; k++ {
			if k < len(a) && d-k < len(b) && a[k] == b[d-k] {
				return k, d - k
			}
		}
	}
	return len(a), len(b)
}

func TestLineDiff(t *testing.T) {
	cases := []struct {
		name, a, b string
		max        int
		want       string
	}{
		{"changed line", "a\nb\nc", "a\nB\nc", 40, "@@ -2,1 +2,1 @@\n-b\n+B"},
		{"inserted lines", "a\nc", "a\nb1\nb2\nc", 40, "@@ -2,0 +2,2 @@\n+b1\n+b2"},
		{"removed tail", "a\nb\nc", "a", 40, "@@ -2,2 +2,0 @@\n-b\n-c"},
		{"two hunks", "a\nb\nc\nd", "A\nb\nc\nD", 40, "@@ -1,1 +1,1 @@\n-a\n+A\n@@ -4,1 +4,1 @@\n-d\n+D"},
		{"capped between hunks", "a\nb\nc\nd", "A\nb\nc\nD", 2, "@@ -1,1 +1,1 @@\n-a\n+A\n... more differences"},
		{"capped inside a hunk", "a\nb\nc", "x\ny\nz", 4, "@@ -1,3 +1,3 @@\n-a\n-b\n-c\n+x\n... more differences"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := "--- api/openapi.yaml (committed)\n+++ api/openapi.yaml (generated)\n" + tc.want
			if got := lineDiff(tc.a, tc.b, tc.max); got != want {
				t.Errorf("lineDiff =\n%s\nwant\n%s", got, want)
			}
		})
	}
}

// loadSpec parses and validates the committed document with kin-openapi.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load %s: %v", specPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate %s: %v", specPath, err)
	}
	return doc
}

func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	doc := loadSpec(t)
	for _, rt := range APIRoutes() {
		item := doc.Paths.Find(rt.Path)
		if item == nil || item.GetOperation(rt.Method) == nil {
			t.Errorf("%s %s is in the route table but not in the spec", rt.Method, rt.Path)
			continue
		}
		op := item.GetOperation(rt.Method)
		_, hasResult := op.Extensions[openapi.ResultExtension]
		if rt.Accepted != hasResult {
			t.Errorf("%s %s: accepted=%v but x-operation-result present=%v", rt.Method, rt.Path, rt.Accepted, hasResult)
		}
		if rt.Accepted && op.Responses.Status(http.StatusAccepted) == nil {
			t.Errorf("%s %s: accepted route without a 202 response", rt.Method, rt.Path)
		}
		if secured := op.Security != nil && len(*op.Security) > 0; secured != (rt.Auth == api.User) {
			t.Errorf("%s %s: security present=%v, auth=%v", rt.Method, rt.Path, secured, rt.Auth)
		}
	}
	sseOp := doc.Paths.Find("/api/v1/sse").Get
	sse := sseOp.Responses.Status(http.StatusOK).Value.Content
	if mt := sse.Get("text/event-stream"); mt == nil || mt.Schema.Ref != "#/components/schemas/Notification" {
		t.Errorf("SSE route must document text/event-stream with the Notification schema, got %+v", sse)
	}
	for _, d := range []string{doc.Info.Description, sseOp.Description} {
		for _, want := range []string{"BEFORE dispatching", "not buffered or replayed", "ref", "server_status", "apps_status"} {
			if !strings.Contains(d, want) {
				t.Errorf("SSE delivery semantics: %q lacks %q", d, want)
			}
		}
	}
}

// component returns a named schema of the committed document.
func component(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("no component %s", name)
	}
	return ref.Value
}

func visitBytes(s *openapi3.Schema, body []byte) error {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return err
	}
	return s.VisitJSON(v, openapi3.EnableJSONSchema2020())
}

// TestOpenAPIRejectsIncompleteResponses pins that response schemas are not
// permissive: a body missing a field the server always writes fails.
func TestOpenAPIRejectsIncompleteResponses(t *testing.T) {
	doc := loadSpec(t)
	servers := doc.Paths.Find("/api/v1/server/get-servers").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value
	for _, body := range []string{
		`{"success":true,"data":{}}`,
		`{"success":true,"data":{"servers":[{"whatever":1}]}}`,
	} {
		if err := visitBytes(servers, []byte(body)); err == nil {
			t.Errorf("get-servers accepts %s", body)
		}
	}
	if err := visitBytes(component(t, doc, "GetAppResponse"), []byte(`{"app":{"app_id":"a"}}`)); err == nil {
		t.Error("get-app result accepts an app without config/variables/files")
	}
	bad := model.Notification{Type: model.NotificationServerStatus, Payload: map[string]int{"server_id": 1}}
	b, _ := json.Marshal(bad)
	if err := visitBytes(component(t, doc, "Notification"), b); err == nil {
		t.Errorf("Notification accepts a server_status payload of the wrong shape: %s", b)
	}
}

// TestOpenAPIRequestsLenient pins that request schemas accept what the
// handlers accept: missing fields and a null or absent config.
func TestOpenAPIRequestsLenient(t *testing.T) {
	doc := loadSpec(t)
	save := doc.Paths.Find("/api/v1/app/save-app").Post.RequestBody.Value.Content.Get("application/json").Schema.Value
	for _, body := range []string{
		`{"server_id":"s","app":{"id":"a"}}`,
		`{"server_id":"s","app":{"id":"a"},"config":null}`,
		`{"server_id":"s","app":{"id":"a"},"config":{"k":"v"},"files":null,"variables":[]}`,
	} {
		if err := visitBytes(save, []byte(body)); err != nil {
			t.Errorf("save-app request %s: %v", body, err)
		}
	}
}

// TestOpenAPIMatchesResultsAndEvents validates real encodings of
// x-operation-result payloads and SSE notifications.
func TestOpenAPIMatchesResultsAndEvents(t *testing.T) {
	doc := loadSpec(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	result := json.RawMessage(`{"app":{"app_id":"a","config":null,"variables":null,"files":null}}`)
	cases := []struct {
		name, schema string
		value        any
	}{
		{"get-app result, nil config", "GetAppResponse", command.GetAppResponse{App: command.AppPayload{AppID: "a"}}},
		{"get-app result, full", "GetAppResponse", command.GetAppResponse{App: command.AppPayload{
			AppID: "a", Config: []byte(`{"name":"x"}`),
			Variables: []command.ContentItem{{Name: "K", Content: []byte("v")}},
			Files:     []command.ContentItem{},
		}}},
		{"server_status", "Notification", model.Notification{
			Type: model.NotificationServerStatus, Timestamp: now,
			Payload: model.ServerStatusPayload{ServerID: "s", Liveness: "online"},
		}},
		{"apps_status", "Notification", model.Notification{
			Type: model.NotificationAppsStatus, Timestamp: now,
			Payload: command.AppsStatusPayload{ServerID: "s", Apps: []command.AppStatus{{AppID: "a"}}},
		}},
		{"apps_status, nil apps", "Notification", model.Notification{
			Type: model.NotificationAppsStatus, Timestamp: now,
			Payload: command.AppsStatusPayload{ServerID: "s"},
		}},
		{"operation_result", "Notification", model.Notification{
			Type: model.NotificationOperationResult, Ref: "req-1", Timestamp: now, Payload: result,
		}},
		{"operation_result failed", "Notification", model.Notification{
			Type: model.NotificationOperationResult, Ref: "req-1", Timestamp: now,
			Status: model.NotificationStatusError, Error: "agent offline",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := visitBytes(component(t, doc, tc.schema), b); err != nil {
				t.Fatalf("%s does not match %s: %v", b, tc.schema, err)
			}
		})
	}
}

// TestOpenAPIMatchesWire writes real webutil responses and validates the
// bytes against the committed schemas, so the envelope in the document is
// the envelope on the wire.
func TestOpenAPIMatchesWire(t *testing.T) {
	doc := loadSpec(t)
	cases := []struct {
		name, method, path string
		status             int
		contentType        string
		write              func(w http.ResponseWriter)
	}{
		{"success without data", http.MethodGet, "/_/health", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "healthy", nil)
		}},
		{"success with data", http.MethodGet, "/api/v1/auth/state", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "auth state", hauth.StateResponse{Bootstrap: true, RegistrationEnabled: true})
		}},
		{"success with empty message", http.MethodGet, "/api/v1/domains/check", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "", happ.CheckDomainResponse{Available: true})
		}},
		{"servers", http.MethodGet, "/api/v1/server/get-servers", 200, "application/json", func(w http.ResponseWriter) {
			seen := time.Now()
			util.Success(w, "servers", server.GetServersResponse{Servers: []model.Server{
				{ID: "s1", OrganizationID: "o", Name: "n", CreatedAt: seen},
				{ID: "s2", LastSeenAt: &seen, Capabilities: []model.Capability{{Name: "k", Value: "v"}}, Features: map[string]bool{"ingress": true}},
			}})
		}},
		{"no servers", http.MethodGet, "/api/v1/server/get-servers", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "servers", server.GetServersResponse{})
		}},
		{"nil list", http.MethodGet, "/api/v1/user/get-tokens", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "tokens", []model.UserToken(nil))
		}},
		{"accepted", http.MethodPost, "/api/v1/app/save-app", 202, "application/json", func(w http.ResponseWriter) {
			util.Accepted(w, "app save dispatched", struct {
				RequestID string `json:"request_id"`
			}{"req-1"})
		}},
		{"bad request", http.MethodPost, "/api/v1/app/save-app", 400, "application/json", func(w http.ResponseWriter) {
			util.Error(w, "server_id is required", nil)
		}},
		{"unauthorized", http.MethodGet, "/api/v1/server/get-servers", 401, "application/json", func(w http.ResponseWriter) {
			util.Unauthorized(w)
		}},
		{"forbidden", http.MethodGet, "/api/v1/registry/list", 403, "application/json", func(w http.ResponseWriter) {
			util.Forbidden(w)
		}},
		{"rate limited", http.MethodPost, "/api/v1/auth/register", 429, "text/plain", func(w http.ResponseWriter) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
		}},
		{"not found", http.MethodGet, "/api/v1/template/get-template", 404, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusNotFound, "template not found")
		}},
		{"conflict", http.MethodPost, "/api/v1/template/delete-template", 409, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusConflict, "the template has live instances")
		}},
		{"agent failed", http.MethodPost, "/api/v1/instance/create-instance", 502, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusBadGateway, "pull access denied")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.write(rec)
			if rec.Code != tc.status {
				t.Fatalf("writer status = %d, want %d", rec.Code, tc.status)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
				t.Fatalf("writer Content-Type = %q, want %s", ct, tc.contentType)
			}
			resp := doc.Paths.Find(tc.path).GetOperation(tc.method).Responses.Status(tc.status)
			if resp == nil {
				t.Fatalf("%s %s documents no %d", tc.method, tc.path, tc.status)
			}
			mt := resp.Value.Content.Get(tc.contentType)
			if mt == nil {
				t.Fatalf("%s %s %d documents no %s body", tc.method, tc.path, tc.status, tc.contentType)
			}
			var body any = rec.Body.String()
			if tc.contentType == "application/json" {
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body is not JSON: %v", err)
				}
			}
			if err := mt.Schema.Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatalf("wire bytes %s do not match the schema: %v", rec.Body.String(), err)
			}
		})
	}
}
