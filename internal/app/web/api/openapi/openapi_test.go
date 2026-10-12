package openapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"winterflow/internal/app/web/api"
)

type thing struct {
	Name string `json:"name"`
}

type thingResult struct {
	ID string `json:"id"`
}

// thingList exercises the response-side rules: required non-omitempty
// fields, null for nil-able kinds, omitempty fields optional.
type thingList struct {
	Items  []thing           `json:"items"`
	Blob   []byte            `json:"blob"`
	Ptr    *thing            `json:"ptr"`
	Tags   map[string]string `json:"tags"`
	Note   string            `json:"note,omitempty"`
	Later  *thing            `json:"later,omitempty"`
	Hidden string            `json:"-"`
}

// saveThing is request-only: it keeps its plain name, while thing, which
// is also a response type, is documented request-side as thingInput.
type saveThing struct {
	Thing  thing           `json:"thing"`
	Config json.RawMessage `json:"config"`
}

// event is an SSE envelope keyed by type, like model.Notification.
type event struct {
	Type    string `json:"type"`
	Ref     string `json:"ref"`
	Payload any    `json:"payload,omitempty"`
}

type thingForm struct {
	Name string `formData:"name"`
}

type avatarPath struct {
	Avatar string `path:"avatar"`
}

type thingQuery struct {
	ServerID string `query:"server_id" required:"true"`
}

func noop(http.ResponseWriter, *http.Request) {}

// sample covers every response shape the generator knows.
func sample() []api.Route {
	return []api.Route{
		{Method: http.MethodGet, Path: "/_/health", Handler: noop, Summary: "health", Tag: "system"},
		{Method: http.MethodGet, Path: "/api/v1/thing/get-thing", Handler: noop, Auth: api.User,
			Summary: "get", Tag: "things", Query: thingQuery{}, Response: thing{},
			Errors: []int{http.StatusBadRequest, http.StatusForbidden, http.StatusBadRequest}},
		{Method: http.MethodPost, Path: "/api/v1/thing/save-thing", Handler: noop, Auth: api.User,
			Summary: "save", Tag: "things", Request: saveThing{}, Accepted: true, Result: thingResult{},
			Errors: []int{http.StatusBadRequest}},
		// A second operation with the same Result reuses the component.
		{Method: http.MethodPost, Path: "/api/v1/thing/copy-thing", Handler: noop, Auth: api.User,
			Summary: "copy", Tag: "things", Accepted: true, Result: thingResult{}},
		{Method: http.MethodGet, Path: "/api/v1/thing/list-things", Handler: noop, Auth: api.User,
			Summary: "list", Tag: "things", Response: thingList{}},
		{Method: http.MethodGet, Path: "/api/v1/sse", Handler: noop, Auth: api.User,
			Summary: "events", Description: "Subscribe before dispatching.", Tag: "system", Events: event{},
			EventPayloads: []api.EventPayload{{Type: "op"}, {Type: "thing", Payload: thing{}}}},
		{Method: http.MethodPost, Path: "/auth/local/login", Mounted: true, Raw: true,
			Summary: "login", Tag: "auth", Request: thing{}, Form: thingForm{}, Response: thingResult{},
			Errors: []int{http.StatusForbidden, http.StatusTooManyRequests}},
		{Method: http.MethodGet, Path: "/avatar/{avatar}", Mounted: true, Raw: true,
			Summary: "avatar", Tag: "auth", Query: avatarPath{}, ContentType: "image/*"},
	}
}

// load builds routes and parses the YAML back with kin-openapi, the
// validator PR 3's contract suite uses.
func load(t *testing.T, routes []api.Route) *openapi3.T {
	t.Helper()
	out, err := YAML(routes)
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(out)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	return doc
}

func schemaFor(t *testing.T, doc *openapi3.T, method, path string, status int, contentType string) *openapi3.Schema {
	t.Helper()
	item := doc.Paths.Find(path)
	if item == nil {
		t.Fatalf("no path %s", path)
	}
	op := item.GetOperation(method)
	if op == nil {
		t.Fatalf("no %s %s", method, path)
	}
	resp := op.Responses.Status(status)
	if resp == nil || resp.Value == nil {
		t.Fatalf("%s %s: no %d response", method, path, status)
	}
	mt := resp.Value.Content.Get(contentType)
	if mt == nil || mt.Schema == nil {
		t.Fatalf("%s %s %d: no %s content", method, path, status, contentType)
	}
	return mt.Schema.Value
}

func visit(s *openapi3.Schema, body string) error {
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return err
	}
	return s.VisitJSON(v, openapi3.EnableJSONSchema2020())
}

func TestBuildValidDocument(t *testing.T) {
	doc := load(t, sample())
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", doc.OpenAPI)
	}
	for _, name := range []string{SchemeBearer, SchemeCookie} {
		if doc.Components.SecuritySchemes[name] == nil {
			t.Errorf("missing security scheme %q", name)
		}
	}
	if got := len(doc.Paths.Map()); got != 8 {
		t.Errorf("paths = %d, want 8", got)
	}
	if !strings.Contains(doc.Info.Description, "BEFORE dispatching") {
		t.Errorf("info.description does not document SSE delivery: %q", doc.Info.Description)
	}
	if d := doc.Paths.Find("/api/v1/sse").Get.Description; d != "Subscribe before dispatching." {
		t.Errorf("route description = %q", d)
	}
}

func TestEnvelopeSchemas(t *testing.T) {
	doc := load(t, sample())
	cases := []struct {
		name, method, path string
		status             int
		contentType, body  string
		ok                 bool
	}{
		{"null data", "GET", "/_/health", 200, "application/json", `{"success":true,"message":"healthy","data":null}`, true},
		{"null data, no message", "GET", "/_/health", 200, "application/json", `{"success":true,"data":null}`, true},
		{"null data rejects payload", "GET", "/_/health", 200, "application/json", `{"success":true,"data":{"x":1}}`, false},
		{"typed data", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true,"data":{"name":"n"}}`, true},
		{"typed data missing field", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true,"data":{}}`, false},
		{"list", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"items":[{"name":"n"}],"blob":"AQ==","ptr":{"name":"p"},"tags":{"a":"b"},"note":"x","later":{"name":"l"}}}`, true},
		{"list nil kinds are null", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"items":null,"blob":null,"ptr":null,"tags":null}}`, true},
		{"list item missing field", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"items":[{"whatever":1}],"blob":null,"ptr":null,"tags":null}}`, false},
		{"list missing items", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"blob":null,"ptr":null,"tags":null}}`, false},
		{"omitempty pointer is not null", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"items":null,"blob":null,"ptr":null,"tags":null,"later":null}}`, false},
		{"unknown fields stay allowed", "GET", "/api/v1/thing/list-things", 200, "application/json",
			`{"success":true,"data":{"items":null,"blob":null,"ptr":null,"tags":null,"new":1}}`, true},
		{"typed data wrong type", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true,"data":{"name":1}}`, false},
		{"success must be true", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":false,"data":{"name":"n"}}`, false},
		{"data required", "GET", "/api/v1/thing/get-thing", 200, "application/json", `{"success":true}`, false},
		{"accepted", "POST", "/api/v1/thing/save-thing", 202, "application/json", `{"success":true,"message":"m","data":{"request_id":"r"}}`, true},
		{"accepted needs request_id", "POST", "/api/v1/thing/save-thing", 202, "application/json", `{"success":true,"data":{}}`, false},
		{"error", "POST", "/api/v1/thing/save-thing", 400, "application/json", `{"success":false,"message":"bad","data":null}`, true},
		{"error must be unsuccessful", "POST", "/api/v1/thing/save-thing", 400, "application/json", `{"success":true,"message":"bad","data":null}`, false},
		{"401 json", "GET", "/api/v1/thing/get-thing", 401, "application/json", `{"success":false,"message":"unauthorized","data":null}`, true},
		{"401 text", "GET", "/api/v1/thing/get-thing", 401, "text/plain", `"Unauthorized\n"`, true},
		{"403", "GET", "/api/v1/thing/get-thing", 403, "application/json", `{"success":false,"message":"forbidden","data":null}`, true},
		{"raw body", "POST", "/auth/local/login", 200, "application/json", `{"id":"u1"}`, true},
		{"library error", "POST", "/auth/local/login", 403, "application/json", `{"error":"incorrect user or password"}`, true},
		{"library error as text", "POST", "/auth/local/login", 403, "text/plain", `"{\"error\":\"user is nil\"}\n"`, true},
		{"429 text", "POST", "/auth/local/login", 429, "text/plain", `"too many requests\n"`, true},
		{"sse event", "GET", "/api/v1/sse", 200, "text/event-stream", `{"type":"thing","ref":"","payload":{"name":"n"}}`, true},
		{"sse event needs ref", "GET", "/api/v1/sse", 200, "text/event-stream", `{"type":"thing","payload":{"name":"n"}}`, false},
		{"sse payload follows type", "GET", "/api/v1/sse", 200, "text/event-stream", `{"type":"thing","ref":"","payload":{"name":1}}`, false},
		{"sse payload of result is open", "GET", "/api/v1/sse", 200, "text/event-stream", `{"type":"op","ref":"r","payload":{"name":1}}`, true},
		{"sse unknown type", "GET", "/api/v1/sse", 200, "text/event-stream", `{"type":"other","ref":""}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := visit(schemaFor(t, doc, tc.method, tc.path, tc.status, tc.contentType), tc.body)
			if tc.ok && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want invalid, got valid")
			}
		})
	}
}

func TestErrorStatuses(t *testing.T) {
	doc := load(t, sample())
	get := doc.Paths.Find("/api/v1/thing/get-thing").Get
	for _, code := range []int{400, 401, 403} {
		if get.Responses.Status(code) == nil {
			t.Errorf("get-thing: missing %d", code)
		}
	}
	if n := get.Responses.Len(); n != 4 { // 200, 400, 401, 403: duplicates collapse
		t.Errorf("get-thing responses = %d, want 4", n)
	}
	health := doc.Paths.Find("/_/health").Get
	if health.Responses.Status(401) != nil {
		t.Error("public route documents 401")
	}
	if health.Security != nil && len(*health.Security) > 0 {
		t.Error("public route has security requirements")
	}
	if sec := get.Security; sec == nil || len(*sec) != 2 {
		t.Errorf("user route security = %v, want bearer or cookie", sec)
	}
}

func TestRequestBodies(t *testing.T) {
	doc := load(t, sample())
	body := doc.Paths.Find("/auth/local/login").Post.RequestBody.Value.Content
	if mt := body.Get("application/json"); mt == nil || mt.Schema.Ref != "#/components/schemas/thingInput" {
		t.Errorf("login JSON body = %+v, want $ref thingInput", mt)
	}
	if mt := body.Get("application/x-www-form-urlencoded"); mt == nil || mt.Schema.Ref != "#/components/schemas/FormDatathingForm" {
		t.Errorf("login form body = %+v, want $ref FormDatathingForm", mt)
	}
	avatar := doc.Paths.Find("/avatar/{avatar}").Get
	if p := avatar.Parameters.GetByInAndName("path", "avatar"); p == nil || !p.Required {
		t.Errorf("avatar path param = %+v", p)
	}
	if avatar.Responses.Status(http.StatusOK).Value.Content.Get("image/*") == nil {
		t.Error("avatar must document an image/* body")
	}
}

// TestRequestSchemasLenient pins that request bodies keep the handlers'
// leniency: a type that is also a response type gets a separate Input
// component without the response-side required list.
func TestRequestSchemasLenient(t *testing.T) {
	doc := load(t, sample())
	mt := doc.Paths.Find("/api/v1/thing/save-thing").Post.RequestBody.Value.Content.Get("application/json")
	if mt == nil || mt.Schema.Ref != "#/components/schemas/saveThing" {
		t.Fatalf("save-thing body = %+v, want $ref saveThing (request-only types keep their name)", mt)
	}
	for _, body := range []string{`{}`, `{"thing":{}}`, `{"config":null}`, `{"config":{"a":1}}`} {
		if err := visit(mt.Schema.Value, body); err != nil {
			t.Errorf("request %s: %v", body, err)
		}
	}
	if got := doc.Components.Schemas["thingInput"].Value.Required; len(got) != 0 {
		t.Errorf("thingInput.required = %v, want none", got)
	}
	if got := doc.Components.Schemas["thing"].Value.Required; len(got) != 1 || got[0] != "name" {
		t.Errorf("thing.required = %v, want [name]", got)
	}
}

func TestEventPayloads(t *testing.T) {
	doc := load(t, sample())
	n := doc.Components.Schemas["event"].Value
	d := n.Properties["payload"].Value.Description
	for _, want := range []string{"op:", "x-operation-result", "thing: #/components/schemas/thing"} {
		if !strings.Contains(d, want) {
			t.Errorf("payload description %q lacks %q", d, want)
		}
	}
}

func TestQueryParameters(t *testing.T) {
	doc := load(t, sample())
	p := doc.Paths.Find("/api/v1/thing/get-thing").Get.Parameters.GetByInAndName("query", "server_id")
	if p == nil || !p.Required {
		t.Fatalf("server_id query param = %+v, want required", p)
	}
}

func TestOperationResultExtension(t *testing.T) {
	doc := load(t, sample())
	op := doc.Paths.Find("/api/v1/thing/save-thing").Post
	ext, ok := op.Extensions[ResultExtension].(map[string]any)
	if !ok {
		t.Fatalf("x-operation-result = %#v", op.Extensions[ResultExtension])
	}
	ref, _ := ext["$ref"].(string)
	if ref != "#/components/schemas/thingResult" {
		t.Fatalf("$ref = %q", ref)
	}
	s := doc.Components.Schemas[strings.TrimPrefix(ref, componentsSchemas)]
	if s == nil || s.Value.Properties["id"] == nil {
		t.Fatalf("result component missing or wrong: %+v", s)
	}
	if err := visit(s.Value, `{}`); err == nil {
		t.Error("result without id validates; non-omitempty result fields must be required")
	}
	if err := visit(s.Value, `{"id":"x"}`); err != nil {
		t.Errorf("result with id: %v", err)
	}
	if _, ok := doc.Paths.Find("/api/v1/thing/get-thing").Get.Extensions[ResultExtension]; ok {
		t.Error("synchronous route carries x-operation-result")
	}
}

func TestBuildErrors(t *testing.T) {
	type other struct {
		Name int `json:"name"`
	}
	// thingInput would collide with the request-side name of thing.
	type thingInput struct {
		X int `json:"x"`
	}
	type unsupported struct {
		C chan int `json:"c"`
	}
	collide := func() any {
		// A second type that is also named "thing".
		type thing struct {
			X int `json:"x"`
		}
		return thing{}
	}
	cases := []struct {
		name   string
		routes []api.Route
		want   string
	}{
		{"accepted without result", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true},
		}, "no Result"},
		{"unnamed result", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true, Result: struct{ A int }{}},
		}, "not a named type"},
		{"name clash", []api.Route{
			{Method: "GET", Path: "/a", Response: thing{}},
			{Method: "GET", Path: "/b", Response: collide()},
		}, `schema name "thing"`},
		{"input name clash", []api.Route{
			{Method: "GET", Path: "/a", Response: thing{}},
			{Method: "POST", Path: "/b", Request: thing{}},
			{Method: "POST", Path: "/c", Request: thingInput{}},
		}, `schema name "thingInput"`},
		{"unsupported result field", []api.Route{
			{Method: "POST", Path: "/a", Accepted: true, Result: unsupported{}},
		}, "type is not supported"},
		{"event payloads without events", []api.Route{
			{Method: "GET", Path: "/a", EventPayloads: []api.EventPayload{{Type: "x"}}},
		}, "EventPayloads without Events"},
		{"event payloads on a shapeless event", []api.Route{
			{Method: "GET", Path: "/a", Events: thing{}, EventPayloads: []api.EventPayload{{Type: "x", Payload: thingResult{}}}},
		}, `no "payload" property`},
		{"unnamed event payload", []api.Route{
			{Method: "GET", Path: "/a", Events: event{}, EventPayloads: []api.EventPayload{{Type: "x", Payload: struct{ A int }{}}}},
		}, "not a named type"},
		{"unnamed events", []api.Route{
			{Method: "GET", Path: "/a", Events: struct{ A int }{}, EventPayloads: []api.EventPayload{{Type: "x"}}},
		}, "not a named type"},
		{"duplicate operation", []api.Route{
			{Method: "GET", Path: "/a", Response: other{}},
			{Method: "GET", Path: "/a", Response: other{}},
		}, "already exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.routes)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestOperationID(t *testing.T) {
	for path, want := range map[string]string{
		"/api/v1/app/save-app":    "appSaveApp",
		"/api/v1/sse":             "sse",
		"/_/health":               "health",
		"/auth/local/login":       "authLocalLogin",
		"/avatar/{avatar}":        "avatar",
		"/api/v1/app/get-apps--x": "appGetAppsX",
	} {
		if got := operationID(path); got != want {
			t.Errorf("operationID(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestYAMLReportsBuildErrors(t *testing.T) {
	if _, err := YAML([]api.Route{{Method: "POST", Path: "/a", Accepted: true}}); err == nil {
		t.Fatal("YAML over an invalid table returned no error")
	}
}

func TestYAMLDeterministic(t *testing.T) {
	a, err := YAML(sample())
	if err != nil {
		t.Fatal(err)
	}
	b, err := YAML(sample())
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("two runs over the same routes differ")
	}
}

// things is a named slice: a named type in its own right.
type things []thing

type cyclic struct {
	Next *cyclic   `json:"next"`
	Skip saveThing `json:"-"`
}

func TestResponseTypes(t *testing.T) {
	got := responseTypes([]api.Route{
		{Response: things{}},
		{Result: cyclic{}},
		{Request: saveThing{}, Response: map[string]thingResult{}},
		{Events: event{}, EventPayloads: []api.EventPayload{{Type: "op"}, {Type: "list", Payload: thingList{}}}},
	})
	for _, v := range []any{things{}, thing{}, cyclic{}, thingResult{}, event{}, thingList{}} {
		if !got[reflect.TypeOf(v)] {
			t.Errorf("%T is reachable from a response but not collected", v)
		}
	}
	if got[reflect.TypeOf(saveThing{})] {
		t.Error("saveThing is request-only (and json:\"-\" in cyclic) but was collected")
	}
}

// An Accepted route with a Response documents both answers: 202 with the
// request id, and 200 with the Response when the call waited.
func TestAcceptedRouteThatCanWait(t *testing.T) {
	const path = "/api/v1/thing/make-thing"
	route := api.Route{Method: http.MethodPost, Path: path, Handler: noop, Auth: api.User,
		Summary: "make", Tag: "things", Request: saveThing{}, Accepted: true, Result: thingResult{}, Response: thing{}}
	doc := load(t, []api.Route{route})
	if err := visit(schemaFor(t, doc, http.MethodPost, path, http.StatusAccepted, "application/json"),
		`{"success":true,"data":{"request_id":"r"}}`); err != nil {
		t.Fatalf("202: %v", err)
	}
	if err := visit(schemaFor(t, doc, http.MethodPost, path, http.StatusOK, "application/json"),
		`{"success":true,"data":{"name":"x"}}`); err != nil {
		t.Fatalf("200: %v", err)
	}
	route.Response = nil
	if load(t, []api.Route{route}).Paths.Find(path).Post.Responses.Status(http.StatusOK) != nil {
		t.Fatal("an Accepted route without a Response documents a 200")
	}
}
