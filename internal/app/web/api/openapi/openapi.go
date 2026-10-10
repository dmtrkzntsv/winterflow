// Package openapi renders the api route table into an OpenAPI 3.1 document
// (api/openapi.yaml, written by cmd/openapi). It is the one place that knows
// the wire envelope: every JSON response is wrapped exactly the way
// internal/app/web/util writes it.
package openapi

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/swaggest/jsonschema-go"
	oapi "github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	"winterflow/internal/app/web/api"
)

// Security scheme names. Every User route accepts either.
const (
	SchemeBearer = "bearer"
	SchemeCookie = "cookie"
)

// ResultExtension is the operation extension that names, as a $ref, the
// payload of the SSE operation_result an Accepted route eventually produces.
const ResultExtension = "x-operation-result"

const componentsSchemas = "#/components/schemas/"

// Null is the schema of `data` when a route answers without a payload:
// webutil writes "data":null.
type Null struct{}

// JSONSchema implements jsonschema.Exposer.
func (Null) JSONSchema() (jsonschema.Schema, error) {
	var s jsonschema.Schema
	s.AddType(jsonschema.Null)
	return s, nil
}

// Error is the body of every webutil error: util.Error (400),
// util.Unauthorized (401), util.Forbidden and rbac.RequireAdmin (403).
type Error struct {
	Success bool   `json:"success" required:"true" const:"false"`
	Message string `json:"message" required:"true"`
	Data    Null   `json:"data" required:"true"`
}

// LibraryError is the error body of the go-pkgz /auth and /avatar routes.
type LibraryError struct {
	Error string `json:"error" required:"true"`
}

// AcceptedData is the `data` of every 202: the request id that the SSE
// operation_result will carry as its ref.
type AcceptedData struct {
	RequestID string `json:"request_id" required:"true"`
}

// envelope returns a value of an unnamed struct type shaped like
// util.APIResponse, with Data typed as data's type (Null when data is nil).
// Unnamed, so the schema is inlined per operation while Data's type stays a
// shared component.
func envelope(data any) any {
	dt := reflect.TypeOf(Null{})
	if data != nil {
		dt = reflect.TypeOf(data)
	}
	t := reflect.StructOf([]reflect.StructField{
		{Name: "Success", Type: reflect.TypeOf(true), Tag: `json:"success" required:"true" const:"true"`},
		{Name: "Message", Type: reflect.TypeOf(""), Tag: `json:"message,omitempty"`},
		{Name: "Data", Type: dt, Tag: `json:"data" required:"true"`},
	})
	return reflect.New(t).Elem().Interface()
}

// builder holds one generation run.
type builder struct {
	r *openapi31.Reflector
	// names maps each component name to the Go type that claimed it, so two
	// types with the same name fail loudly instead of one silently
	// shadowing the other.
	names map[string]reflect.Type
	err   error
}

// Build renders routes into an OpenAPI 3.1 document.
func Build(routes []api.Route) (*openapi31.Spec, error) {
	b := &builder{r: openapi31.NewReflector(), names: map[string]reflect.Type{}}
	b.r.DefaultOptions = append(b.r.DefaultOptions, jsonschema.InterceptDefName(b.defName))

	s := b.r.SpecEns()
	s.SetTitle("Winterflow API")
	s.SetVersion("1")
	s.SetDescription("Generated from the route tables in internal/app/web by `make openapi`; do not edit.\n\n" +
		"JSON responses use the envelope {success, message, data}. 202 responses carry data.request_id; " +
		"the outcome arrives on GET /api/v1/sse as an operation_result Notification whose ref is that " +
		"request_id and whose payload matches the operation's x-operation-result schema.")
	s.SetHTTPBearerTokenSecurity(SchemeBearer, "wfp_ personal access token",
		"Personal access token (wfp_…) as `Authorization: Bearer`. It is also accepted as the Basic-auth password.")
	s.SetAPIKeySecurity(SchemeCookie, "JWT", oapi.InCookie,
		"JWT session cookie set by POST /auth/local/login (the X-JWT header is accepted too).")

	for _, rt := range routes {
		if err := b.addRoute(rt); err != nil {
			return nil, fmt.Errorf("%s %s: %w", rt.Method, rt.Path, err)
		}
	}
	if b.err != nil {
		return nil, b.err
	}
	return s, nil
}

// defName names components after the bare Go type ("SaveAppRequest", not
// the package-prefixed "ServerSaveAppRequest") and records clashes. The
// reflector adds its own "FormData" prefix for form bodies afterwards.
func (b *builder) defName(t reflect.Type, _ string) string {
	name := t.Name()
	if prev, ok := b.names[name]; ok && prev != t && b.err == nil {
		b.err = fmt.Errorf("schema name %q used by both %s and %s", name, prev, t)
	}
	b.names[name] = t
	return name
}

func (b *builder) addRoute(rt api.Route) error {
	oc, err := b.r.NewOperationContext(rt.Method, rt.Path)
	if err != nil {
		return err
	}
	oc.SetID(operationID(rt.Path))
	oc.SetSummary(rt.Summary)
	if rt.Tag != "" {
		oc.SetTags(rt.Tag)
	}
	if rt.Auth == api.User {
		oc.AddSecurity(SchemeBearer)
		oc.AddSecurity(SchemeCookie)
	}
	if rt.Query != nil {
		oc.AddReqStructure(rt.Query)
	}
	if rt.Request != nil {
		oc.AddReqStructure(rt.Request)
	}
	if rt.Form != nil {
		oc.AddReqStructure(rt.Form, oapi.WithContentType("application/x-www-form-urlencoded"))
	}

	switch {
	case rt.Events != nil:
		oc.AddRespStructure(rt.Events, oapi.WithContentType("text/event-stream"), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Raw:
		oc.AddRespStructure(rt.Response, oapi.WithContentType(rt.ContentType), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Accepted:
		oc.AddRespStructure(envelope(AcceptedData{}), oapi.WithHTTPStatus(http.StatusAccepted))
	default:
		oc.AddRespStructure(envelope(rt.Response), oapi.WithHTTPStatus(http.StatusOK))
	}
	for _, code := range errorCodes(rt) {
		addError(oc, code, rt.Raw)
	}

	if rt.Accepted {
		if rt.Result == nil {
			return fmt.Errorf("accepted route has no Result")
		}
		ref, err := b.component(rt.Result)
		if err != nil {
			return err
		}
		oc.(openapi31.OperationExposer).Operation().
			WithMapOfAnythingItem(ResultExtension, map[string]any{"$ref": ref})
	}
	return b.r.AddOperation(oc)
}

// errorCodes is rt.Errors plus 401 on User routes, sorted and de-duplicated.
func errorCodes(rt api.Route) []int {
	codes := append([]int(nil), rt.Errors...)
	if rt.Auth == api.User {
		codes = append(codes, http.StatusUnauthorized)
	}
	sort.Ints(codes)
	out := codes[:0]
	for i, c := range codes {
		if i == 0 || codes[i-1] != c {
			out = append(out, c)
		}
	}
	return out
}

// addError documents one error status. 401 comes from webutil (JSON) or
// from the go-pkgz JWT middleware (text/plain "Unauthorized"); 429 is the
// rate limiter's text/plain body; library routes answer {"error": "..."}.
func addError(oc oapi.OperationContext, code int, raw bool) {
	switch {
	case code == http.StatusTooManyRequests:
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	case raw:
		oc.AddRespStructure(LibraryError{}, oapi.WithHTTPStatus(code))
	case code == http.StatusUnauthorized:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	default:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
	}
}

// component reflects v into components/schemas and returns its $ref.
func (b *builder) component(v any) (string, error) {
	s, err := b.r.Reflect(v,
		jsonschema.RootRef,
		jsonschema.DefinitionsPrefix(componentsSchemas),
		jsonschema.CollectDefinitions(func(name string, schema jsonschema.Schema) {
			comps := b.r.SpecEns().ComponentsEns()
			if _, ok := comps.Schemas[name]; ok {
				return
			}
			sm, err := schema.ToSchemaOrBool().ToSimpleMap()
			if err != nil && b.err == nil {
				b.err = err
			}
			comps.WithSchemasItem(name, sm)
		}),
	)
	if err != nil {
		return "", err
	}
	if s.Ref == nil {
		return "", fmt.Errorf("result type %T is not a named type", v)
	}
	return *s.Ref, nil
}

// operationID derives a stable id from the path: /api/v1/app/save-app ->
// appSaveApp, /_/health -> health, /avatar/{avatar} -> avatar.
func operationID(path string) string {
	path = strings.TrimPrefix(path, "/api/v1")
	var out strings.Builder
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "_" || strings.HasPrefix(seg, "{") {
			continue
		}
		for _, word := range strings.Split(seg, "-") {
			if word == "" {
				continue
			}
			if out.Len() == 0 {
				out.WriteString(word)
				continue
			}
			out.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return out.String()
}

// YAML renders routes as the committed api/openapi.yaml bytes.
func YAML(routes []api.Route) ([]byte, error) {
	s, err := Build(routes)
	if err != nil {
		return nil, err
	}
	return s.MarshalYAML()
}
