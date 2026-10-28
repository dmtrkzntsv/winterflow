package oauthlogin

import (
	"context"
	"crypto/sha1" //nolint:gosec // matches the previous auth library's account keys, not a security use
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"winterflow/internal/domain/model"
)

// loginPage is where a failed sign-in lands (with ?error=<code>).
const loginPage = "/login"

// Error codes a callback's redirect carries as ?error=.
const (
	ErrCodeInvalidState          = "invalid_state"
	ErrCodeDenied                = "denied"
	ErrCodeProvider              = "provider_error"
	ErrCodeEmailRequired         = "email_required"
	ErrCodeEmailUnverified       = "email_unverified"
	ErrCodeAccountExists         = "account_exists"
	ErrCodeRegistrationClosed    = "registration_closed"
	ErrCodeSessionMismatch       = "session_mismatch"
	ErrCodeIdentityTaken         = "identity_taken"
	ErrCodeProviderAlreadyLinked = "provider_already_linked"
)

// RegistrationPolicy says whether a sign-in may create an account; firstOnly
// limits that to the instance's first user (the claim step).
type RegistrationPolicy func(ctx context.Context) (open, firstOnly bool)

// CallbackInput is what reached the callback: the provider's code, state
// and error (query parameters, or Apple's form fields, AppleUser being its
// one-time JSON "user" field carrying the name), the flow cookie, and the
// signed-in user ("" when the request carries no session).
type CallbackInput struct {
	Code, State, Error, AppleUser string
	Flow                          string
	SessionUserID                 string
	Registration                  RegistrationPolicy
}

// CallbackResult is where to send the browser, and the user to start a
// session for ("" when none: a link, or a failure).
type CallbackResult struct {
	Redirect string
	SignIn   string
}

// Callback resolves the provider's answer. It never fails: every failure
// becomes a redirect carrying an error code, the cause going to the log.
func (s *Service) Callback(ctx context.Context, provider string, in CallbackInput) CallbackResult {
	st, err := s.consumeState(ctx, provider, in.State, in.Flow)
	if err != nil {
		s.warn("state", err, provider)
		return CallbackResult{Redirect: withError(loginPage, ErrCodeInvalidState)}
	}
	fail := func(code string) CallbackResult { return CallbackResult{Redirect: errorURL(st, code)} }
	if in.Error != "" {
		return fail(ErrCodeDenied)
	}
	p, err := s.provider(provider)
	if err != nil {
		return fail(ErrCodeProvider)
	}
	now := s.now()
	toks, err := p.Client.Exchange(ctx, in.Code, st.CodeVerifier, s.RedirectURI(provider), now)
	if err != nil {
		s.warn("exchange", err, provider)
		return fail(ErrCodeProvider)
	}
	claims, err := p.Client.VerifyIDToken(ctx, toks.IDToken, st.Nonce, now)
	if err != nil {
		s.warn("id token", err, provider)
		return fail(ErrCodeProvider)
	}
	if claims.Email == "" && toks.AccessToken != "" {
		claims = s.userinfoFallback(ctx, p, toks.AccessToken, claims)
	}
	if claims.Name == "" && in.AppleUser != "" {
		claims.Name = appleName(in.AppleUser)
	}
	if claims.Email == "" {
		return fail(ErrCodeEmailRequired)
	}
	if !claims.EmailVerified && !p.Client.TrustsEmail() {
		return fail(ErrCodeEmailUnverified)
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if err := s.adoptLegacy(ctx, provider, claims.Subject); err != nil {
		s.warn("legacy identity", err, provider)
		return fail(ErrCodeProvider)
	}
	id := model.Identity{Provider: provider, Subject: claims.Subject, Email: email, Issuer: p.Client.IssuerURL(), CreatedAt: now}
	if st.Intent == model.IntentLink {
		return s.link(ctx, st, in, id)
	}
	return s.login(ctx, st, in, id, claims.Name)
}

// consumeState deletes the state and checks it was started for this
// provider, has not expired, and by this browser (the flow cookie). A
// state presented twice, or with the wrong flow secret, is gone either way.
func (s *Service) consumeState(ctx context.Context, provider, state, flow string) (model.LoginState, error) {
	if state == "" {
		return model.LoginState{}, errors.New("state missing")
	}
	st, err := s.repo.ConsumeLoginState(ctx, sha256Hex(state))
	if err != nil {
		return model.LoginState{}, err
	}
	switch {
	case st.Provider != provider:
		return model.LoginState{}, errors.New("state provider mismatch")
	case !s.now().Before(st.ExpiresAt):
		return model.LoginState{}, errors.New("state expired")
	case flow == "" || subtle.ConstantTimeCompare([]byte(sha256Hex(flow)), []byte(st.FlowHash)) != 1:
		return model.LoginState{}, errors.New("flow secret mismatch")
	}
	return st, nil
}

// errorURL reports a failure where the flow started: a link back on its
// return page, a sign-in on the login page, carrying the sign-in's return
// path as ?next= so a retry resumes where it was headed (an
// /oauth/authorize request, say).
func errorURL(st model.LoginState, code string) string {
	if st.Intent == model.IntentLink {
		return withError(st.ReturnTo, code)
	}
	return loginErrorURL(st.ReturnTo, code)
}

// userinfoFallback fills email/verified/name only where the ID token left
// them empty, and only when userinfo's sub matches. Failure is not fatal.
func (s *Service) userinfoFallback(ctx context.Context, p Provider, accessToken string, claims model.ProviderClaims) model.ProviderClaims {
	info, err := p.Client.UserInfo(ctx, accessToken)
	if err != nil {
		s.warn("userinfo", err, p.ID)
		return claims
	}
	if info.Subject != claims.Subject {
		return claims
	}
	if claims.Email == "" {
		claims.Email = info.Email
		claims.EmailVerified = info.EmailVerified
	}
	if claims.Name == "" {
		claims.Name = info.Name
	}
	return claims
}

// appleName extracts "firstName lastName" from Apple's first-sign-in user
// field.
func appleName(raw string) string {
	var u struct {
		Name struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
		} `json:"name"`
	}
	if json.Unmarshal([]byte(raw), &u) != nil {
		return ""
	}
	return strings.TrimSpace(u.Name.FirstName + " " + u.Name.LastName)
}

// adoptLegacy moves a Google account linked by the previous auth library
// (keyed by the hex SHA-1 of the subject) to the subject itself, so its
// owner signs in to the same account.
func (s *Service) adoptLegacy(ctx context.Context, provider, subject string) error {
	if provider != model.ProviderGoogle {
		return nil
	}
	if _, err := s.repo.IdentityBySubject(ctx, provider, subject); err == nil {
		return nil
	} else if !errors.Is(err, model.ErrIdentityNotFound) {
		return err
	}
	sum := sha1.Sum([]byte(subject)) //nolint:gosec // see the import
	_, err := s.repo.RekeyIdentity(ctx, provider, hex.EncodeToString(sum[:]), subject)
	return err
}

// login signs in the identity's user, creating one when the identity is new
// and registration is open.
func (s *Service) login(ctx context.Context, st model.LoginState, in CallbackInput, id model.Identity, name string) CallbackResult {
	fail := func(code string) CallbackResult { return CallbackResult{Redirect: errorURL(st, code)} }
	ok := func(userID string) CallbackResult { return CallbackResult{Redirect: st.ReturnTo, SignIn: userID} }

	existing, err := s.repo.IdentityBySubject(ctx, id.Provider, id.Subject)
	if err == nil && !existing.SameIssuer(id.Issuer) {
		// The same subject at another issuer is another person: the slot
		// was repointed at a different IdP. Never sign them in to the
		// account linked through the old one.
		s.log.Warn("oauth callback: identity linked through another issuer",
			"provider", id.Provider, "stored_issuer", existing.Issuer, "issuer", id.Issuer, "user_id", existing.UserID)
		return fail(ErrCodeProvider)
	}
	if err == nil {
		if existing.Email != id.Email {
			if err := s.repo.UpdateIdentityEmail(ctx, id.Provider, id.Subject, id.Email); err != nil {
				s.warn("identity email", err, id.Provider)
			}
		}
		return ok(existing.UserID)
	}
	if !errors.Is(err, model.ErrIdentityNotFound) {
		s.warn("identity lookup", err, id.Provider)
		return fail(ErrCodeProvider)
	}

	// An address an account already answers to is never taken over: the
	// provider may not be the address's owner's, and even when it is, the
	// owner links it from their settings, signed in.
	inUse, err := s.repo.EmailInUse(ctx, id.Email)
	if err != nil {
		s.warn("email lookup", err, id.Provider)
		return fail(ErrCodeProvider)
	}
	if inUse {
		return fail(ErrCodeAccountExists)
	}
	open, firstOnly := false, false
	if in.Registration != nil {
		open, firstOnly = in.Registration(ctx)
	}
	if !open {
		return fail(ErrCodeRegistrationClosed)
	}
	u, err := s.users.CreateProviderUser(ctx, deriveName(name, id.Email), id, firstOnly)
	if errors.Is(err, model.ErrNotBootstrap) {
		return fail(ErrCodeRegistrationClosed)
	}
	if errors.Is(err, model.ErrEmailTaken) {
		return fail(ErrCodeAccountExists)
	}
	if err != nil {
		s.warn("create user", err, id.Provider)
		return fail(ErrCodeProvider)
	}
	s.log.Info("oauth sign-in: account created", "provider", id.Provider, "user_id", u.ID)
	return ok(u.ID)
}

// link attaches the identity to the user who started the link, who must
// also be the one signed in on this browser.
func (s *Service) link(ctx context.Context, st model.LoginState, in CallbackInput, id model.Identity) CallbackResult {
	if in.SessionUserID == "" || in.SessionUserID != st.LinkUserID {
		return CallbackResult{Redirect: errorURL(st, ErrCodeSessionMismatch)}
	}
	id.UserID = st.LinkUserID
	switch err := s.repo.LinkIdentity(ctx, id); {
	case err == nil:
		return CallbackResult{Redirect: st.ReturnTo}
	case errors.Is(err, model.ErrIdentityTaken):
		return CallbackResult{Redirect: errorURL(st, ErrCodeIdentityTaken)}
	case errors.Is(err, model.ErrProviderAlreadyLinked):
		return CallbackResult{Redirect: errorURL(st, ErrCodeProviderAlreadyLinked)}
	default:
		s.warn("link", err, id.Provider)
		return CallbackResult{Redirect: errorURL(st, ErrCodeProvider)}
	}
}

func (s *Service) warn(what string, err error, provider string) {
	s.log.Warn("oauth callback: "+what, "error", err.Error(), "provider", provider)
}
