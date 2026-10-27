package authserver

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

func TestExchange(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, err := f.exchange(cid, code)
	if err != nil || !strings.HasPrefix(tr.AccessToken, "wfo_") || !strings.HasPrefix(tr.RefreshToken, "wfr_") ||
		tr.TokenType != "Bearer" || tr.ExpiresIn != 3600 || tr.Scope != "mcp" {
		t.Fatalf("%+v %v", tr, err)
	}
	p, err := f.tokens.Authenticate(f.ctx, tr.AccessToken)
	if err != nil || p.UserID != f.user || p.Kind != model.TokenOAuth || p.Scope != model.ScopeMCP {
		t.Fatalf("access token principal: %+v %v", p, err)
	}
	_, err = f.exchange(cid, code)
	wantOAuth(t, err, "invalid_grant") // single use
	if l := f.connected(t); len(l) != 1 {
		t.Fatalf("a reused code must not make a grant: %d grants", len(l))
	}
}

func TestExchange_Rejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		mut  func(*model.TokenRequest)
		want string
	}{
		{"wrong verifier", func(r *model.TokenRequest) { r.CodeVerifier = strings.Repeat("a", 43) }, "invalid_grant"},
		{"wrong redirect", func(r *model.TokenRequest) { r.RedirectURI = "http://localhost/cb" }, "invalid_grant"},
		{"wrong resource", func(r *model.TokenRequest) { r.Resource = "https://other.test/mcp" }, "invalid_target"},
		{"wrong client", func(r *model.TokenRequest) {
			// Registered, so it authenticates and consumes the code; an
			// unknown id would fail before the code is touched.
			r.ClientID = f.register(t, "", callback).ClientID
		}, "invalid_grant"},
	}
	for _, tc := range cases {
		cid, code := f.approve(t)
		req := model.TokenRequest{GrantType: "authorization_code", ClientID: cid, Code: code, RedirectURI: callback, CodeVerifier: verifier}
		tc.mut(&req)
		_, err := f.s.Token(f.ctx, req)
		if oe := new(OAuthError); !errors.As(err, &oe) || oe.Code != tc.want {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.want)
		}
		if _, err := f.exchange(cid, code); err == nil {
			t.Errorf("%s: a failed attempt must burn the code", tc.name)
		}
	}
	cid, code := f.approve(t)
	f.clock.Advance(CodeTTL + time.Second)
	_, err := f.exchange(cid, code)
	wantOAuth(t, err, "invalid_grant") // expired
	if l := f.connected(t); len(l) != 0 {
		t.Fatal("no rejected exchange may leave a grant")
	}
}

func TestRefresh_RotationGraceAndTheft(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	first, _ := f.exchange(cid, code)
	second, err := f.refresh(cid, first.RefreshToken)
	if err != nil || second.RefreshToken == first.RefreshToken || second.AccessToken == "" {
		t.Fatalf("%+v %v", second, err)
	}
	// A concurrent loser within the grace window: refused, the grant stays.
	f.clock.Advance(10 * time.Second)
	_, err = f.refresh(cid, first.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
	third, err := f.refresh(cid, second.RefreshToken)
	if err != nil {
		t.Fatalf("the grant must survive a grace-window replay: %v", err)
	}
	// A replay after the grace window is theft: the grant goes with its
	// access tokens.
	list := f.connected(t)
	f.clock.Advance(RefreshGrace + time.Second)
	_, err = f.refresh(cid, second.RefreshToken) // rotated away by the refresh above
	wantOAuth(t, err, "invalid_grant")
	if l := f.connected(t); len(l) != 0 {
		t.Fatal("theft must revoke the grant")
	}
	if len(f.tokens.revoked) != 1 || f.tokens.revoked[0] != list[0].GrantID {
		t.Fatalf("revoked tokens of %v", f.tokens.revoked)
	}
	if f.authenticates(third.AccessToken) {
		t.Fatal("the stolen grant's access token must stop working")
	}
}

func TestRefresh_ReplayOfOlderTokenRevokesAtAnyAge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after time.Duration
	}{{"inside grace", 10 * time.Second}, {"after grace", RefreshGrace + time.Second}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			cid, code := f.approve(t)
			r0, _ := f.exchange(cid, code)
			r1, err := f.refresh(cid, r0.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			r2, err := f.refresh(cid, r1.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			list := f.connected(t)
			f.clock.Advance(tc.after)
			_, err = f.refresh(cid, r0.RefreshToken)
			wantOAuth(t, err, "invalid_grant")
			if l := f.connected(t); len(l) != 0 {
				t.Fatal("a replay of a token two rotations old must revoke the grant")
			}
			if len(f.tokens.revoked) != 1 || f.tokens.revoked[0] != list[0].GrantID {
				t.Fatalf("revoked tokens of %v", f.tokens.revoked)
			}
			_, err = f.refresh(cid, r2.RefreshToken)
			wantOAuth(t, err, "invalid_grant")
		})
	}
}

func TestRefresh_GraceOnlyForTheLatestRotatedToken(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	r0, _ := f.exchange(cid, code)
	r1, err := f.refresh(cid, r0.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(10 * time.Second)
	_, err = f.refresh(cid, r0.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
	if len(f.tokens.revoked) != 0 {
		t.Fatal("a grace-window replay of the latest rotated token must not revoke")
	}
	if _, err := f.refresh(cid, r1.RefreshToken); err != nil {
		t.Fatalf("the grant must survive a grace-window replay: %v", err)
	}
}

func TestRefresh_ReplayAfterBackwardClockStep(t *testing.T) {
	for _, tc := range []struct {
		name    string
		back    time.Duration
		revoked bool
	}{{"back 10 minutes", 10 * time.Minute, true}, {"back 10 seconds", 10 * time.Second, false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			cid, code := f.approve(t)
			r0, _ := f.exchange(cid, code)
			r1, err := f.refresh(cid, r0.RefreshToken)
			if err != nil {
				t.Fatal(err)
			}
			f.clock.Advance(-tc.back)
			_, err = f.refresh(cid, r0.RefreshToken)
			wantOAuth(t, err, "invalid_grant")
			list := f.connected(t)
			if tc.revoked {
				if len(list) != 0 || len(f.tokens.revoked) != 1 {
					t.Fatalf("a replay after a large backward clock step must revoke the grant: %v %v", list, f.tokens.revoked)
				}
				return
			}
			if len(list) != 1 || len(f.tokens.revoked) != 0 {
				t.Fatal("a small backward step stays inside the grace window")
			}
			if _, err := f.refresh(cid, r1.RefreshToken); err != nil {
				t.Fatalf("the grant must survive: %v", err)
			}
		})
	}
}

func TestRefresh_ExpiryAndClientBinding(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, _ := f.exchange(cid, code)
	other := f.register(t, "", "https://a.test/cb")
	_, err := f.refresh(other.ClientID, tr.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
	f.clock.Advance(GrantIdleTTL + time.Hour)
	_, err = f.refresh(cid, tr.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
}

func TestToken_ClientAuth(t *testing.T) {
	f := newFixture(t)
	c, _ := f.s.Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{callback}, TokenEndpointAuthMethod: "client_secret_post"})
	code := f.approveFor(t, c.ClientID)
	_, err := f.s.Token(f.ctx, model.TokenRequest{GrantType: "authorization_code", ClientID: c.ClientID, Code: code,
		RedirectURI: callback, CodeVerifier: verifier, ClientSecret: "nope"})
	wantOAuth(t, err, "invalid_client")
	var oe *OAuthError
	errors.As(err, &oe)
	if oe.Status != 401 {
		t.Fatal("invalid_client is 401")
	}
	_, err = f.s.Token(f.ctx, model.TokenRequest{GrantType: "authorization_code", ClientID: "not-a-uuid", Code: code,
		RedirectURI: callback, CodeVerifier: verifier})
	wantOAuth(t, err, "invalid_client")
	_, err = f.s.Token(f.ctx, model.TokenRequest{GrantType: "authorization_code", ClientID: util.GenerateID(), Code: code,
		RedirectURI: callback, CodeVerifier: verifier})
	wantOAuth(t, err, "invalid_client")
	_, err = f.s.Token(f.ctx, model.TokenRequest{GrantType: "password"})
	wantOAuth(t, err, "unsupported_grant_type")
	_, err = f.off().Token(f.ctx, model.TokenRequest{GrantType: "authorization_code"})
	if !errors.As(err, &oe) || oe.Status != 404 {
		t.Fatalf("disabled: %v", err)
	}
}

func TestToken_ConfidentialClientSucceeds(t *testing.T) {
	f := newFixture(t)
	c, err := f.s.Register(f.ctx, model.ClientRegistration{RedirectURIs: []string{callback}, TokenEndpointAuthMethod: "client_secret_post"})
	if err != nil || c.ClientSecret == "" {
		t.Fatalf("%+v %v", c, err)
	}
	code := f.approveFor(t, c.ClientID)
	tr, err := f.s.Token(f.ctx, model.TokenRequest{GrantType: "authorization_code", ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		Code: code, RedirectURI: callback, CodeVerifier: verifier})
	if err != nil || tr.RefreshToken == "" {
		t.Fatalf("%+v %v", tr, err)
	}
	_, err = f.refresh(c.ClientID, tr.RefreshToken)
	wantOAuth(t, err, "invalid_client") // a refresh needs the secret too
	if _, err := f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: c.ClientID, ClientSecret: c.ClientSecret, RefreshToken: tr.RefreshToken}); err != nil {
		t.Fatal(err)
	}
}

func TestToken_ParameterErrors(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	full := model.TokenRequest{GrantType: "authorization_code", ClientID: cid, Code: code, RedirectURI: callback, CodeVerifier: verifier}
	for name, m := range map[string]func(*model.TokenRequest){
		"code":          func(r *model.TokenRequest) { r.Code = "" },
		"redirect_uri":  func(r *model.TokenRequest) { r.RedirectURI = "" },
		"code_verifier": func(r *model.TokenRequest) { r.CodeVerifier = "" },
	} {
		req := full
		m(&req)
		_, err := f.s.Token(f.ctx, req)
		wantOAuth(t, err, "invalid_request")
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// None of the rejections above touched the store: the code is still good.
	if _, err := f.s.Token(f.ctx, full); err != nil {
		t.Fatalf("a malformed request must not burn the code: %v", err)
	}
	req := full
	req.ClientID = ""
	_, err := f.s.Token(f.ctx, req)
	wantOAuth(t, err, "invalid_client")
	_, err = f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: cid})
	wantOAuth(t, err, "invalid_request")
}

func TestRefresh_ResourceAndScope(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, _ := f.exchange(cid, code)
	_, err := f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: cid, RefreshToken: tr.RefreshToken, Resource: "https://other.test/mcp"})
	wantOAuth(t, err, "invalid_target")
	// The refusal above leaves the token usable; any requested scope is
	// tolerated and the grant stays mcp.
	tr, err = f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: cid, RefreshToken: tr.RefreshToken, Resource: testURL + "/mcp/", Scope: "admin"})
	if err != nil || tr.Scope != "mcp" {
		t.Fatalf("%+v %v", tr, err)
	}
	if _, err := f.s.Token(f.ctx, model.TokenRequest{GrantType: "refresh_token", ClientID: cid, RefreshToken: tr.RefreshToken, Scope: "mcp"}); err != nil {
		t.Fatal(err)
	}
}

func TestExchange_ReplacesEarlierGrantForSameClient(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	first, err := f.exchange(cid, code)
	if err != nil {
		t.Fatal(err)
	}
	other, otherCode := f.approve(t)
	if _, err := f.exchange(other, otherCode); err != nil {
		t.Fatal(err)
	}
	before, _ := f.repo.ListUnrevokedGrants(f.ctx, f.user)
	if len(before) != 2 {
		t.Fatalf("two clients, two grants: %d", len(before))
	}
	firstGrant := before[0].Grant.ID
	if before[0].Grant.ClientID != cid {
		firstGrant = before[1].Grant.ID
	}

	if _, err := f.exchange(cid, f.approveFor(t, cid)); err != nil {
		t.Fatal(err)
	}
	after, _ := f.repo.ListUnrevokedGrants(f.ctx, f.user)
	if len(after) != 2 {
		t.Fatalf("re-authorizing must replace, not add: %d grants", len(after))
	}
	for _, g := range after {
		if g.Grant.ID == firstGrant {
			t.Fatal("the earlier grant for the same client must be revoked")
		}
	}
	if len(f.tokens.revoked) != 1 || f.tokens.revoked[0] != firstGrant {
		t.Fatalf("the replaced grant's access tokens must be revoked: %v", f.tokens.revoked)
	}
	if f.authenticates(first.AccessToken) {
		t.Fatal("the replaced grant's access token must stop working")
	}
	_, err = f.refresh(cid, first.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
}

func TestRefresh_HousekeepingPurgesDeadRows(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, err := f.exchange(cid, code)
	if err != nil {
		t.Fatal(err)
	}
	old := f.clock.Now().Add(-60 * 24 * time.Hour)
	dead := model.OAuthGrant{ID: util.GenerateID(), UserID: f.user, ClientID: cid, RefreshTokenHash: "dead-hash",
		CreatedAt: old, LastUsedAt: old, ExpiresAt: old, RevokedAt: &old}
	if err := f.repo.InsertGrant(f.ctx, dead); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.InsertCode(f.ctx, model.OAuthCode{CodeHash: "stale-code", ClientID: cid, UserID: f.user,
		RedirectURI: "x", CodeChallenge: "c", Resource: "r", CreatedAt: old, ExpiresAt: old}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(HousekeepingInterval)
	if _, err := f.refresh(cid, tr.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.GetGrant(f.ctx, dead.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("a refresh must purge long-dead grants: %v", err)
	}
	if _, err := f.repo.ConsumeCode(f.ctx, "stale-code"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("a refresh must purge expired codes: %v", err)
	}
}

func TestHousekeepingDue_ThrottledAndRaceFree(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	var ran atomic.Int64
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.s.housekeepingDue(f.clock.Now()) {
				ran.Add(1)
			}
		}()
	}
	wg.Wait()
	if ran.Load() != 1 {
		t.Fatalf("concurrent callers inside one interval must elect exactly one runner, got %d", ran.Load())
	}
	f.clock.Advance(HousekeepingInterval - time.Second)
	if f.s.housekeepingDue(f.clock.Now()) {
		t.Fatal("a call inside the interval must not run")
	}
	f.clock.Advance(time.Second)
	if !f.s.housekeepingDue(f.clock.Now()) {
		t.Fatal("a call after the interval must run")
	}
}

func TestHousekeepingDue_ClockStepBackwards(t *testing.T) {
	f := newFixture(t)
	if !f.s.housekeepingDue(f.clock.Now()) {
		t.Fatal("the first call must run")
	}
	f.clock.Advance(-time.Hour)
	if !f.s.housekeepingDue(f.clock.Now()) {
		t.Fatal("a clock stepped backwards must count as due")
	}
	f.clock.Advance(time.Minute)
	if f.s.housekeepingDue(f.clock.Now()) {
		t.Fatal("the throttle must restart from the stepped-back time")
	}
}

// TestApproveCodeCarriesIssuer: the redirect names the issuer (RFC 9207),
// so a client talking to several servers can tell the answers apart.
func TestApproveCodeCarriesIssuer(t *testing.T) {
	f := newFixture(t)
	c := f.register(t, "Claude", callback)
	to, err := f.s.ApproveAuthorization(f.ctx, f.user, authReq(c.ClientID))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(to)
	if u.Query().Get("iss") != testURL {
		t.Fatalf("iss = %q", u.Query().Get("iss"))
	}
}
