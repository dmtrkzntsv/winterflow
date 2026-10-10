package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"winterflow/internal/app/web"
	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/api/openapi"
)

// specPath is the committed, generated document (PR 2), relative to this
// package.
var specPath = filepath.Join("..", "..", "..", "..", "api", "openapi.yaml")

var (
	specOnce sync.Once
	specDoc  *openapi3.T
	specErr  error
	table    = web.APIRoutes()
)

// loadSpec parses api/openapi.yaml once per test binary.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	specOnce.Do(func() {
		specDoc, specErr = openapi3.NewLoader().LoadFromFile(specPath)
		if specErr == nil {
			specErr = specDoc.Validate(context.Background())
		}
	})
	if specErr != nil {
		t.Fatalf("load %s: %v", specPath, specErr)
	}
	return specDoc
}

// matchPath reports whether a concrete URL path matches a table path, where
// a {param} segment matches any one non-empty segment.
func matchPath(tpl, path string) (map[string]string, bool) {
	ts, ps := strings.Split(tpl, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return nil, false
	}
	params := map[string]string{}
	for i := range ts {
		if strings.HasPrefix(ts[i], "{") && strings.HasSuffix(ts[i], "}") {
			if ps[i] == "" {
				return nil, false
			}
			params[strings.Trim(ts[i], "{}")] = ps[i]
			continue
		}
		if ts[i] != ps[i] {
			return nil, false
		}
	}
	return params, true
}

// routeFor finds the table route serving method + path.
func routeFor(method, path string) (api.Route, map[string]string, bool) {
	for _, rt := range table {
		if rt.Method != method {
			continue
		}
		if params, ok := matchPath(rt.Path, path); ok {
			return rt, params, true
		}
	}
	return api.Route{}, nil, false
}

// validationInput builds the kin-openapi input for req; the operation is
// looked up from the table route, so no router dependency is needed.
func validationInput(t *testing.T, req *http.Request) *openapi3filter.RequestValidationInput {
	t.Helper()
	doc := loadSpec(t)
	rt, params, ok := routeFor(req.Method, req.URL.Path)
	if !ok {
		t.Fatalf("%s %s is not in the route table", req.Method, req.URL.Path)
	}
	item := doc.Paths.Find(rt.Path)
	if item == nil || item.GetOperation(rt.Method) == nil {
		t.Fatalf("%s %s is not in %s", rt.Method, rt.Path, specPath)
	}
	return &openapi3filter.RequestValidationInput{
		Request:    req,
		PathParams: params,
		Route: &routers.Route{
			Spec: doc, Path: rt.Path, PathItem: item, Method: rt.Method,
			Operation: item.GetOperation(rt.Method),
		},
		Options: &openapi3filter.Options{
			// Auth is asserted by status codes, not by the validator.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
	}
}

// validateRequest checks req against the document (it restores the body).
func validateRequest(t *testing.T, req *http.Request) {
	t.Helper()
	in := validationInput(t, req)
	if err := openapi3filter.ValidateRequest(req.Context(), in); err != nil {
		t.Fatalf("request %s %s violates the spec: %v", req.Method, req.URL.Path, err)
	}
}

// validateResponse checks status, headers and body against the document; an
// undocumented status fails.
func validateResponse(t *testing.T, req *http.Request, status int, header http.Header, body []byte) {
	t.Helper()
	in := validationInput(t, req)
	in.Options.IncludeResponseStatus = true
	if strings.HasPrefix(header.Get("Content-Type"), "text/event-stream") {
		in.Options.ExcludeResponseBody = true
	}
	err := openapi3filter.ValidateResponse(req.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: in,
		Status:                 status,
		Header:                 header,
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                in.Options,
	})
	if err != nil {
		t.Fatalf("response %d of %s %s violates the spec: %v\nbody: %s", status, req.Method, req.URL.Path, err, body)
	}
}

// validateSchema checks raw JSON against a named component.
func validateSchema(t *testing.T, component string, raw []byte) {
	t.Helper()
	loadSpec(t)
	if err := checkSchema(component, raw); err != nil {
		t.Fatal(err)
	}
}

// checkSchema is validateSchema without a *testing.T (for cleanups); the
// spec must already be loaded.
func checkSchema(component string, raw []byte) error {
	ref := specDoc.Components.Schemas[component]
	if ref == nil {
		return fmt.Errorf("component %s is not in the spec", component)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s is not JSON: %v", raw, err)
	}
	if err := ref.Value.VisitJSON(v, openapi3.EnableJSONSchema2020()); err != nil {
		return fmt.Errorf("%s does not match %s: %v", raw, component, err)
	}
	return nil
}

// resultSchema is the x-operation-result component of an Accepted route.
func resultSchema(t *testing.T, rt api.Route) string {
	t.Helper()
	op := loadSpec(t).Paths.Find(rt.Path).GetOperation(rt.Method)
	ext, ok := op.Extensions[openapi.ResultExtension].(map[string]any)
	if !ok {
		t.Fatalf("%s %s has no %s", rt.Method, rt.Path, openapi.ResultExtension)
	}
	ref, _ := ext["$ref"].(string)
	return strings.TrimPrefix(ref, "#/components/schemas/")
}
