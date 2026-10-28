package authserver

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/authn"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/accesstoken"
	authserverrepo "winterflow/internal/infra/db/repository/authserver"
)

const (
	testURL  = "https://wf.example.test"
	callback = "https://claude.ai/api/mcp/auth_callback"
	// The RFC 7636 appendix B pair.
	challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// tokens is the real authn usecase, recording the grants whose tokens were
// revoked.
type tokens struct {
	*authn.UseCase
	mu      sync.Mutex
	revoked []string
}

func (k *tokens) RevokeGrantTokens(ctx context.Context, grantID string) error {
	k.mu.Lock()
	k.revoked = append(k.revoked, grantID)
	k.mu.Unlock()
	return k.UseCase.RevokeGrantTokens(ctx, grantID)
}

type fixture struct {
	s      *UseCase
	repo   *authserverrepo.Repo
	tokens *tokens
	clock  *clock
	d      *db.DB
	ctx    context.Context
	user   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := authserverrepo.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	at, err := accesstoken.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedOrg(t, d, dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(10), dbtest.ID(1))
	c := &clock{t: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	tk := &tokens{UseCase: authn.New(at, c.Now)}
	return &fixture{
		s: New(repo, tk, d.Tx, testURL, c.Now), repo: repo, tokens: tk, clock: c, d: d,
		ctx: dbtest.Ctx(t), user: dbtest.ID(10),
	}
}

// off is the same server with no issuer.
func (f *fixture) off() *UseCase { return New(f.repo, f.tokens, f.d.Tx, "", f.clock.Now) }

func authReq(clientID string) model.AuthorizationRequest {
	return model.AuthorizationRequest{
		ClientID: clientID, RedirectURI: callback, ResponseType: "code",
		CodeChallenge: challenge, CodeChallengeMethod: "S256",
		Resource: testURL + "/mcp", State: "st&1",
	}
}

func (f *fixture) register(t *testing.T, name string, uris ...string) model.RegisteredClient {
	t.Helper()
	c, err := f.s.Register(f.ctx, model.ClientRegistration{ClientName: name, RedirectURIs: uris})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// approve registers a client and approves it for the user, returning the
// client id and the code.
func (f *fixture) approve(t *testing.T) (clientID, code string) {
	t.Helper()
	c := f.register(t, "Claude", callback)
	return c.ClientID, f.approveFor(t, c.ClientID)
}

func (f *fixture) approveFor(t *testing.T, clientID string) string {
	t.Helper()
	to, err := f.s.ApproveAuthorization(f.ctx, f.user, authReq(clientID))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	return u.Query().Get("code")
}

func (f *fixture) exchange(clientID, code string) (model.TokenResponse, error) {
	return f.s.Token(f.ctx, model.TokenRequest{GrantType: "authorization_code", ClientID: clientID, Code: code,
		RedirectURI: callback, CodeVerifier: verifier, Resource: testURL + "/mcp"})
}

func (f *fixture) refresh(clientID, rt string) (model.TokenResponse, error) {
	return f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: clientID, RefreshToken: rt})
}

// authenticates reports whether the access token still authenticates.
func (f *fixture) authenticates(raw string) bool {
	_, err := f.tokens.Authenticate(f.ctx, raw)
	return err == nil
}

func (f *fixture) connected(t *testing.T) []model.ConnectedApp {
	t.Helper()
	l, err := f.s.ListConnectedApps(f.ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func wantOAuth(t *testing.T, err error, code string) {
	t.Helper()
	var oe *OAuthError
	if !errors.As(err, &oe) || oe.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}
