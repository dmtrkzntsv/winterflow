package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/api/openapi"
	"winterflow/internal/app/web/util"
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
		t.Fatal("api/openapi.yaml is stale: run make openapi and commit the result")
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
	sse := doc.Paths.Find("/api/v1/sse").Get.Responses.Status(http.StatusOK).Value.Content
	if mt := sse.Get("text/event-stream"); mt == nil || mt.Schema.Ref != "#/components/schemas/Notification" {
		t.Errorf("SSE route must document text/event-stream with the Notification schema, got %+v", sse)
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
			util.Success(w, "auth state", struct {
				Bootstrap           bool `json:"bootstrap"`
				RegistrationEnabled bool `json:"registration_enabled"`
			}{true, true})
		}},
		{"success with empty message", http.MethodGet, "/api/v1/domains/check", 200, "application/json", func(w http.ResponseWriter) {
			util.Success(w, "", struct {
				Available bool                `json:"available"`
				Claims    []model.DomainClaim `json:"claims"`
			}{Available: true})
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
		{"go-pkgz unauthorized", http.MethodGet, "/api/v1/server/get-servers", 401, "text/plain", func(w http.ResponseWriter) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}},
		{"forbidden", http.MethodGet, "/api/v1/registry/list", 403, "application/json", func(w http.ResponseWriter) {
			util.Forbidden(w)
		}},
		{"rate limited", http.MethodPost, "/api/v1/auth/register", 429, "text/plain", func(w http.ResponseWriter) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
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
