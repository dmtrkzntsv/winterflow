// Package openapi renders the api route table into an OpenAPI 3.1 document
// (api/openapi.yaml, written by cmd/openapi). It is the one place that knows
// the wire envelope: every JSON response is wrapped exactly the way
// internal/app/web/util writes it.
//
// Response and request schemas follow different rules, because Go's
// encoder and decoder differ:
//
//   - Response side (200/202 data, raw library bodies, SSE events,
//     x-operation-result): every JSON field without omitempty/omitzero is
//     required, since encoding/json always writes it, and a nil-able field
//     (slice, map, pointer, []byte, interface) without omitempty also allows
//     null, since Go encodes nil as null.
//   - Request side (bodies, query, form): only fields tagged
//     `required:"true"` are required; handlers accept missing fields.
//
// A Go type used on both sides gets two components: "<Name>" for the
// response shape and "<Name>Input" for the request shape. A type used on
// one side only keeps its bare name. No schema sets additionalProperties,
// so adding a response field stays a compatible change.
package openapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
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
// util.Unauthorized (401) and util.Forbidden (403, a server of another org).
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

// InputSuffix names the request-side component of a type that is also
// used in responses.
const InputSuffix = "Input"

// Description is the document's info.description.
const Description = "Generated from the route tables in internal/app/web by `make openapi`; do not edit.\n\n" +
	"JSON responses use the envelope {success, message, data}. Response schemas list every field the " +
	"server always writes as required; request schemas require only what the handler rejects without. " +
	"Unknown properties are allowed everywhere, so new response fields are not a breaking change.\n\n" +
	"## Asynchronous operations and GET /api/v1/sse\n\n" +
	"A 202 response means the command was dispatched to the server's agent; its data.request_id names " +
	"the outcome, which arrives only over the event stream:\n\n" +
	"1. Open GET /api/v1/sse BEFORE dispatching. Results are best-effort: they are not buffered or " +
	"replayed, so a result produced while no stream is open, or while the client is not reading, is lost.\n" +
	"2. A result is delivered only to the open streams of the user who dispatched the command (all of " +
	"them, if that user has several).\n" +
	"3. Each event is `data: <Notification JSON>`. Match type == \"operation_result\" and ref == request_id; " +
	"status 1 with error means it failed, otherwise payload matches the operation's x-operation-result schema.\n" +
	"4. Other event types (server_status, apps_status) interleave on the same stream, sent to every member " +
	"of the server's organization; ignore types you do not handle."

// sideKey marks a reflection that produces a response-side schema outside
// an operation (x-operation-result and event payload components).
type sideKey struct{}

func responseSide(rc *jsonschema.ReflectContext) {
	rc.Context = context.WithValue(rc.Context, sideKey{}, true)
}

// isResponse reports whether rc reflects a response body.
func isResponse(rc *jsonschema.ReflectContext) bool {
	if v, _ := rc.Value(sideKey{}).(bool); v {
		return true
	}
	oc, ok := oapi.OperationCtx(rc)
	return ok && oc.IsProcessingResponse() && oc.ProcessingIn() == oapi.InBody
}

// builder holds one generation run.
type builder struct {
	r *openapi31.Reflector
	// names maps each component name to the Go type that claimed it, so two
	// types with the same name fail loudly instead of one silently
	// shadowing the other.
	names map[string]reflect.Type
	// responseTypes holds every named type reachable from a response, so
	// its request-side component can take the Input suffix.
	responseTypes map[reflect.Type]bool
	err           error
}

// Build renders routes into an OpenAPI 3.1 document.
func Build(routes []api.Route) (*openapi31.Spec, error) {
	b := &builder{
		r:             openapi31.NewReflector(),
		names:         map[string]reflect.Type{},
		responseTypes: responseTypes(routes),
	}
	b.r.DefaultOptions = append(b.r.DefaultOptions,
		func(rc *jsonschema.ReflectContext) {
			// Installed per reflection so the name can depend on which
			// side rc reflects.
			jsonschema.InterceptDefName(func(t reflect.Type, _ string) string {
				return b.defName(t, isResponse(rc))
			})(rc)
		},
		jsonschema.InterceptProp(interceptProp),
	)

	s := b.r.SpecEns()
	s.SetTitle("Winterflow API")
	s.SetVersion("1")
	s.SetDescription(Description)
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
// the package-prefixed "ServerSaveAppRequest"), adds InputSuffix to the
// request side of a type that responses use too, and records clashes. The
// reflector adds its own "FormData" prefix for form bodies afterwards.
func (b *builder) defName(t reflect.Type, response bool) string {
	name := t.Name()
	b.claim(name, t)
	if !response && b.responseTypes[t] {
		name += InputSuffix
		b.claim(name, t)
	}
	return name
}

func (b *builder) claim(name string, t reflect.Type) {
	if prev, ok := b.names[name]; ok && prev != t && b.err == nil {
		b.err = fmt.Errorf("schema name %q used by both %s and %s", name, prev, t)
	}
	b.names[name] = t
}

// responseTypes walks every response-side root (Response, Result, Events
// and EventPayloads) and returns the named types reachable from them.
func responseTypes(routes []api.Route) map[reflect.Type]bool {
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			if t.Name() != "" {
				seen[t] = true
			}
			t = t.Elem()
		}
		if seen[t] {
			return
		}
		if t.Name() != "" {
			seen[t] = true
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && f.Tag.Get("json") != "-" {
				walk(f.Type)
			}
		}
	}
	for _, rt := range routes {
		roots := []any{rt.Response, rt.Result, rt.Events}
		for _, ep := range rt.EventPayloads {
			roots = append(roots, ep.Payload)
		}
		for _, v := range roots {
			if v != nil {
				walk(reflect.TypeOf(v))
			}
		}
	}
	return seen
}

// interceptProp applies the response-side rules from the package comment,
// plus one rule for both sides: an open-ended field (interface or
// json.RawMessage) without omitempty documents null explicitly, because
// kin-openapi, unlike JSON Schema, rejects null against {}.
func interceptProp(p jsonschema.InterceptPropParams) error {
	if !p.Processed {
		return nil
	}
	omitted := omittable(p.Field)
	s := p.PropertySchema
	open := s.Type == nil && s.Ref == nil && len(s.AnyOf) == 0 && len(s.OneOf) == 0 && len(s.AllOf) == 0
	if open && !omitted && (p.Field.Type.Kind() == reflect.Interface || p.Field.Type == rawMessage) {
		s.AnyOf = []jsonschema.SchemaOrBool{jsonschema.Null.ToSchemaOrBool(), (&jsonschema.Schema{}).ToSchemaOrBool()}
	}
	if !isResponse(p.Context) || omitted {
		return nil
	}
	if !slices.Contains(p.ParentSchema.Required, p.Name) {
		p.ParentSchema.Required = append(p.ParentSchema.Required, p.Name)
	}
	switch p.Field.Type.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
	default:
		return nil
	}
	switch {
	case s.Ref != nil:
		ref := jsonschema.Schema{Ref: s.Ref}
		s.Ref = nil
		s.AnyOf = []jsonschema.SchemaOrBool{jsonschema.Null.ToSchemaOrBool(), ref.ToSchemaOrBool()}
	case s.Type != nil && !s.HasType(jsonschema.Null):
		s.AddType(jsonschema.Null)
	}
	return nil
}

var rawMessage = reflect.TypeOf(json.RawMessage(nil))

// omittable reports whether encoding/json may leave the field out.
func omittable(f reflect.StructField) bool {
	_, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
	for _, o := range strings.Split(opts, ",") {
		if o == "omitempty" || o == "omitzero" {
			return true
		}
	}
	return false
}

func (b *builder) addRoute(rt api.Route) error {
	oc, err := b.r.NewOperationContext(rt.Method, rt.Path)
	if err != nil {
		return err
	}
	oc.SetID(operationID(rt.Path))
	oc.SetSummary(rt.Summary)
	if rt.Description != "" {
		oc.SetDescription(rt.Description)
	}
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

	if err := b.eventPayloads(rt); err != nil {
		return err
	}

	switch {
	case rt.Events != nil:
		oc.AddRespStructure(rt.Events, oapi.WithContentType("text/event-stream"), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Raw:
		oc.AddRespStructure(rt.Response, oapi.WithContentType(rt.ContentType), oapi.WithHTTPStatus(http.StatusOK))
	case rt.Accepted:
		oc.AddRespStructure(envelope(AcceptedData{}), oapi.WithHTTPStatus(http.StatusAccepted))
		if rt.Response != nil {
			// A route that can also wait for the outcome answers it with 200.
			oc.AddRespStructure(envelope(rt.Response), oapi.WithHTTPStatus(http.StatusOK))
		}
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
		// go-pkgz sometimes calls WriteHeader before rest.RenderJSON sets
		// the Content-Type (/auth/user 401, unknown provider 400), so the
		// same {"error": "..."} also arrives as sniffed text/plain.
		oc.AddRespStructure(LibraryError{}, oapi.WithHTTPStatus(code))
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	case code == http.StatusUnauthorized:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
		oc.AddRespStructure("", oapi.WithContentType("text/plain"), oapi.WithHTTPStatus(code))
	default:
		oc.AddRespStructure(Error{}, oapi.WithHTTPStatus(code))
	}
}

// eventPayloads documents rt.EventPayloads on the Events component: the
// payload property's description lists the payload of each type, and one
// allOf entry per typed payload lets a validator check it.
func (b *builder) eventPayloads(rt api.Route) error {
	if len(rt.EventPayloads) == 0 {
		return nil
	}
	if rt.Events == nil {
		return fmt.Errorf("EventPayloads without Events")
	}
	ref, err := b.component(rt.Events)
	if err != nil {
		return err
	}
	ev := b.r.SpecEns().ComponentsEns().Schemas[strings.TrimPrefix(ref, componentsSchemas)]
	props, _ := ev["properties"].(map[string]any)
	payload, _ := props["payload"].(map[string]any)
	if payload == nil || props["type"] == nil {
		return fmt.Errorf("events type %T has no \"payload\" property or no \"type\" property", rt.Events)
	}
	lines := []string{"Depends on type:"}
	var conds []any
	for _, ep := range rt.EventPayloads {
		if ep.Payload == nil {
			lines = append(lines, fmt.Sprintf("- %s: the x-operation-result schema of the operation whose "+
				"request_id equals ref (absent when the operation failed)", ep.Type))
			continue
		}
		pref, err := b.component(ep.Payload)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", ep.Type, pref))
		// "type is not X, or payload matches P": the implication an
		// if/then states, spelled with keywords kin-openapi enforces.
		conds = append(conds, map[string]any{"anyOf": []any{
			map[string]any{"not": map[string]any{
				"properties": map[string]any{"type": map[string]any{"const": ep.Type}},
				"required":   []any{"type"},
			}},
			map[string]any{"properties": map[string]any{"payload": map[string]any{"$ref": pref}}},
		}})
	}
	payload["description"] = strings.Join(lines, "\n")
	if len(conds) > 0 {
		ev["allOf"] = conds
	}
	return nil
}

// component reflects v, response-side, into components/schemas and returns
// its $ref.
func (b *builder) component(v any) (string, error) {
	s, err := b.r.Reflect(v,
		responseSide,
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
