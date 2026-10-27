package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"winterflow/internal/app/web/middleware/authn"
)

// client is one caller of the API: anonymous, a session cookie, or a PAT
// bearer. Every request and response it sends is validated against
// api/openapi.yaml.
type client struct {
	e      *env
	http   *http.Client
	bearer string
	userID string
	email  string
	stream *stream // SSE subscription, opened by subscribe
}

// call is one request. Body is JSON-encoded unless it is a string, which is
// sent verbatim (malformed-body probes). Invalid skips request validation
// for deliberately bad requests; the response is validated either way.
type call struct {
	Method  string
	Path    string
	Query   url.Values
	Body    any
	Header  http.Header // extra request headers
	Invalid bool
}

// response is a received answer.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// envelope is webutil's body shape.
type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// requestTimeout bounds one non-streaming request, so a handler that hangs
// fails its test instead of the whole run's -timeout.
const requestTimeout = 30 * time.Second

func (e *env) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{
		Jar: jar,
		// A redirect is part of the contract: validate the 3xx itself
		// rather than whatever it points to.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// anon is an unauthenticated client.
func (e *env) anon() *client { return e.newClient() }

func (c *client) do(t *testing.T, k call) *response {
	t.Helper()
	u := c.e.srv.URL + k.Path
	if len(k.Query) > 0 {
		u += "?" + k.Query.Encode()
	}
	var body io.Reader
	switch b := k.Body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(c.e.ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, k.Method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	// As the web UI: every request says it is the app's own (the CSRF
	// check on cookie-authenticated writes). A call's Header may override.
	req.Header.Set(authn.CSRFHeader, "1")
	for k, v := range k.Header {
		req.Header[k] = v
	}
	if !k.Invalid {
		validateRequest(t, req)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", k.Method, k.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	// Validation needs a fresh request: the original body was consumed.
	vreq, _ := http.NewRequestWithContext(c.e.ctx, k.Method, u, nil)
	validateResponse(t, vreq, resp.StatusCode, resp.Header, raw)
	return &response{Status: resp.StatusCode, Header: resp.Header, Body: raw}
}

func (c *client) get(t *testing.T, path string, q url.Values) *response {
	t.Helper()
	return c.do(t, call{Method: http.MethodGet, Path: path, Query: q})
}

func (c *client) post(t *testing.T, path string, body any) *response {
	t.Helper()
	return c.do(t, call{Method: http.MethodPost, Path: path, Body: body})
}

// expect fails unless the status is want.
func (r *response) expect(t *testing.T, want int) *response {
	t.Helper()
	if r.Status != want {
		t.Fatalf("status = %d, want %d; body: %s", r.Status, want, r.Body)
	}
	return r
}

// data decodes the envelope's data into v (v may be nil).
func (r *response) data(t *testing.T, v any) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatalf("body is not the envelope: %v: %s", err, r.Body)
	}
	if v != nil {
		if err := json.Unmarshal(env.Data, v); err != nil {
			t.Fatalf("data: %v: %s", err, env.Data)
		}
	}
	return env
}

// accepted asserts a 202, remembers which route dispatched the request id
// (awaitResult validates against its x-operation-result) and returns it.
func (c *client) accepted(t *testing.T, k call) string {
	t.Helper()
	if c.stream == nil {
		t.Fatal("subscribe before dispatching: the result is only delivered to open streams")
	}
	r := c.do(t, k).expect(t, http.StatusAccepted)
	var d struct {
		RequestID string `json:"request_id"`
	}
	r.data(t, &d)
	if d.RequestID == "" {
		t.Fatalf("202 without request_id: %s", r.Body)
	}
	c.e.rememberRequest(d.RequestID, k.Method+" "+k.Path)
	return d.RequestID
}

const ownerPassword = "owner-password"

// claim runs the first-run flow: register (claims the fresh instance) and
// log in. Signing in auto-claims the pending embedded server. The client
// holds the session cookie and an open SSE stream.
func (e *env) claim(t *testing.T) *client {
	t.Helper()
	c := e.newClient()
	c.email = "owner@example.test"
	r := c.post(t, "/api/v1/auth/register", map[string]string{
		"name": "Owner", "email": c.email, "password": ownerPassword,
	}).expect(t, http.StatusOK)
	var reg struct {
		UserID string `json:"user_id"`
	}
	r.data(t, &reg)
	c.userID = reg.UserID
	c.login(t, c.email, ownerPassword)
	c.subscribe(t)
	return c
}

// login signs in with email and password; the session cookie lands in the
// jar.
func (c *client) login(t *testing.T, email, password string) {
	t.Helper()
	c.tryLogin(t, email, password).expect(t, http.StatusOK)
}

// tryLogin posts a login and returns the answer, whatever it is.
func (c *client) tryLogin(t *testing.T, email, password string) *response {
	t.Helper()
	return c.post(t, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

// pat mints a personal access token as c and returns a bearer-only client
// for the same user (no cookie), subscribed to SSE.
func (c *client) pat(t *testing.T) *client {
	t.Helper()
	var tok struct {
		Token string `json:"token"`
	}
	c.post(t, "/api/v1/user/create-token", map[string]any{"name": "contract"}).
		expect(t, http.StatusOK).data(t, &tok)
	return c.e.bearer(t, tok.Token, c)
}

// bearer is a cookie-less client authenticating with token as owner's user,
// subscribed to SSE.
func (e *env) bearer(t *testing.T, token string, owner *client) *client {
	t.Helper()
	p := e.newClient()
	p.bearer, p.userID, p.email = token, owner.userID, owner.email
	p.subscribe(t)
	return p
}

// member creates a member account in c's org and returns it logged in.
func (c *client) member(t *testing.T) *client {
	t.Helper()
	var created struct {
		UserID       string `json:"user_id"`
		TempPassword string `json:"temp_password"`
	}
	c.post(t, "/api/v1/org/create-user", map[string]string{
		"name": "Member", "email": "member@example.test",
	}).expect(t, http.StatusOK).data(t, &created)
	m := c.e.newClient()
	m.userID, m.email = created.UserID, "member@example.test"
	m.login(t, m.email, created.TempPassword)
	m.subscribe(t)
	return m
}

// serverID is the caller's (auto-claimed) server.
func (c *client) serverID(t *testing.T) string {
	t.Helper()
	var d struct {
		Servers []struct {
			ID string `json:"id"`
		} `json:"servers"`
	}
	c.get(t, "/api/v1/server/get-servers", nil).expect(t, http.StatusOK).data(t, &d)
	if len(d.Servers) == 0 {
		t.Fatal("caller has no server")
	}
	return d.Servers[0].ID
}
