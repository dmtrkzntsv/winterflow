package authserver

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

func TestDescribeAuthorization(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "Claude", callback)
	res, err := f.s.DescribeAuthorization(f.ctx, authReq(c.ClientID))
	if err != nil || res.ClientName != "Claude" || res.RedirectHost != "claude.ai" || res.ErrorRedirectURL != "" {
		t.Fatalf("%+v %v", res, err)
	}
	for _, r := range []string{"", testURL + "/mcp/"} {
		q := authReq(c.ClientID)
		q.Resource = r
		if res, err := f.s.DescribeAuthorization(f.ctx, q); err != nil || res.ErrorRedirectURL != "" {
			t.Errorf("resource %q: %+v %v", r, res, err)
		}
	}
	q := authReq(c.ClientID)
	q.Resource = "https://other.test/mcp"
	if res, _ := f.s.DescribeAuthorization(f.ctx, q); !strings.Contains(res.ErrorRedirectURL, "error=invalid_target") || !strings.Contains(res.ErrorRedirectURL, "state=st%261") {
		t.Errorf("bad resource: %+v", res)
	}
	q = authReq(c.ClientID)
	q.CodeChallengeMethod = "plain"
	if res, _ := f.s.DescribeAuthorization(f.ctx, q); !strings.Contains(res.ErrorRedirectURL, "error=invalid_request") {
		t.Errorf("plain pkce: %+v", res)
	}
	// Any requested scope is tolerated; the grant is always mcp.
	for _, sc := range []string{"admin", "mcp", "openid profile", "mcp:tools"} {
		q = authReq(c.ClientID)
		q.Scope = sc
		if res, err := f.s.DescribeAuthorization(f.ctx, q); err != nil || res.ErrorRedirectURL != "" {
			t.Errorf("scope %q: %+v %v", sc, res, err)
		}
	}
	q = authReq(c.ClientID)
	q.ResponseType = "token"
	if res, _ := f.s.DescribeAuthorization(f.ctx, q); !strings.Contains(res.ErrorRedirectURL, "error=unsupported_response_type") {
		t.Errorf("response type: %+v", res)
	}
	if _, err := f.s.DescribeAuthorization(f.ctx, authReq(util.GenerateID())); !errors.Is(err, model.ErrOAuthClientUnknown) {
		t.Errorf("unknown client: %v", err)
	}
	if _, err := f.s.DescribeAuthorization(f.ctx, authReq("not-a-uuid")); !errors.Is(err, model.ErrOAuthClientUnknown) {
		t.Errorf("malformed client id: %v", err)
	}
	q = authReq(c.ClientID)
	q.RedirectURI = "https://evil.test/cb"
	if _, err := f.s.DescribeAuthorization(f.ctx, q); !errors.Is(err, model.ErrOAuthRedirect) {
		t.Errorf("mismatch: %v", err)
	}
}

func TestLoopbackRedirectRules(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "", "http://localhost:4000/callback")
	q := authReq(c.ClientID)
	q.RedirectURI = "http://localhost:5555/callback"
	res, err := f.s.DescribeAuthorization(f.ctx, q)
	if err != nil || !res.IsLoopback || res.ErrorRedirectURL != "" {
		t.Fatalf("another port must be accepted: %+v %v", res, err)
	}
	q.RedirectURI = "http://127.0.0.1:4000/callback"
	if _, err := f.s.DescribeAuthorization(f.ctx, q); !errors.Is(err, model.ErrOAuthRedirect) {
		t.Fatalf("the host spelling must match: %v", err)
	}
}

func TestApproveAndDecline(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "Claude", callback)
	to, err := f.s.ApproveAuthorization(f.ctx, f.user, authReq(c.ClientID))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	if u.Host != "claude.ai" || u.Query().Get("code") == "" || u.Query().Get("state") != "st&1" || u.Query().Get("iss") != testURL {
		t.Fatalf("%s", to)
	}
	code, err := f.repo.ConsumeCode(f.ctx, hashSecret(u.Query().Get("code")))
	if err != nil || code.UserID != f.user || code.Resource != testURL+"/mcp" || code.ExpiresAt.Sub(code.CreatedAt) != CodeTTL {
		t.Fatalf("stored code: %+v %v", code, err)
	}
	got, err := f.repo.GetClient(f.ctx, c.ClientID)
	if err != nil || got.LastUsedAt == nil {
		t.Fatalf("approve must mark the client used: %+v %v", got, err)
	}

	d, err := f.s.DeclineAuthorization(f.ctx, authReq(c.ClientID))
	if err != nil || !strings.Contains(d, "error=access_denied") || !strings.Contains(d, "state=st%261") {
		t.Fatal(d, err)
	}
	bad := authReq(c.ClientID)
	bad.CodeChallengeMethod = "plain"
	r, err := f.s.ApproveAuthorization(f.ctx, f.user, bad)
	if err != nil || !strings.Contains(r, "error=invalid_request") || strings.Contains(r, "code=") {
		t.Fatalf("an invalid request must redirect with an error, not a code: %q %v", r, err)
	}
	if _, err := f.s.DeclineAuthorization(f.ctx, authReq(util.GenerateID())); !errors.Is(err, model.ErrOAuthClientUnknown) {
		t.Fatalf("decline unknown client: %v", err)
	}
	if _, err := f.off().ApproveAuthorization(f.ctx, f.user, authReq(c.ClientID)); !errors.Is(err, model.ErrAuthServerDisabled) {
		t.Fatal(err)
	}
}

func TestRedirectKeepsExistingQuery(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "", "https://a.test/cb?app=1")
	q := authReq(c.ClientID)
	q.RedirectURI = "https://a.test/cb?app=1"
	to, err := f.s.ApproveAuthorization(f.ctx, f.user, q)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	if u.Query().Get("app") != "1" || u.Query().Get("code") == "" {
		t.Fatal(to)
	}
}

func TestErrorRedirectCarriesHostLabel(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "Claude", callback)
	q := authReq(c.ClientID)
	q.Resource = "https://other.test/mcp"
	res, err := f.s.DescribeAuthorization(f.ctx, q)
	if err != nil || res.ErrorRedirectURL == "" || res.ClientName != "Claude" || res.RedirectHost != "claude.ai" || res.IsLoopback {
		t.Fatalf("%+v %v", res, err)
	}
	lb := f.register(t, "Codex", "http://127.0.0.1:1/cb")
	q = authReq(lb.ClientID)
	q.RedirectURI = "http://127.0.0.1:9/cb"
	q.ResponseType = "token"
	res, err = f.s.DescribeAuthorization(f.ctx, q)
	if err != nil || res.ErrorRedirectURL == "" || !res.IsLoopback || res.RedirectHost != "127.0.0.1" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestChallengeAndStateShape(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "Claude", callback)
	for name, mut := range map[string]func(*model.AuthorizationRequest){
		"empty challenge":    func(q *model.AuthorizationRequest) { q.CodeChallenge = "" },
		"short challenge":    func(q *model.AuthorizationRequest) { q.CodeChallenge = "abc" },
		"long challenge":     func(q *model.AuthorizationRequest) { q.CodeChallenge += "A" },
		"padded challenge":   func(q *model.AuthorizationRequest) { q.CodeChallenge = q.CodeChallenge[:42] + "=" },
		"non-base64url char": func(q *model.AuthorizationRequest) { q.CodeChallenge = q.CodeChallenge[:42] + "+" },
		"oversized state":    func(q *model.AuthorizationRequest) { q.State = strings.Repeat("s", 1025) },
	} {
		q := authReq(c.ClientID)
		mut(&q)
		res, err := f.s.DescribeAuthorization(f.ctx, q)
		if err != nil || !strings.Contains(res.ErrorRedirectURL, "error=invalid_request") {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	q := authReq(c.ClientID)
	q.State = strings.Repeat("s", 1025)
	if res, _ := f.s.DescribeAuthorization(f.ctx, q); strings.Contains(res.ErrorRedirectURL, "state=") {
		t.Errorf("a rejected state must not be echoed: %s", res.ErrorRedirectURL)
	}
	q = authReq(c.ClientID)
	q.State = strings.Repeat("s", 1024)
	if res, err := f.s.DescribeAuthorization(f.ctx, q); err != nil || res.ErrorRedirectURL != "" {
		t.Errorf("state at the cap must pass: %+v %v", res, err)
	}
}
