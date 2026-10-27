// Package auth serves sign-in and registration: POST login, logout and
// register, GET state. Registration is the only way accounts self-create;
// login is verify-only. A login or a registration hands the browser a
// session cookie (middleware/authn); logout revokes that session. Policy: a fresh instance (zero users) always accepts the
// first registration (the "claim step" — owner of a new org); afterwards
// standalone is closed (single-org: admins provision accounts at
// /org/members) and distributed follows REGISTRATION_ENABLED (own org per
// registrant).
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// UserStore is the slice of port.UserService this handler needs.
type UserStore interface {
	CountUsers(ctx context.Context) (int, error)
	BootstrapLocalAdmin(ctx context.Context, name, email, password string) (model.User, error)
	RegisterLocalUser(ctx context.Context, name, email, password string) (model.User, error)
	VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
}

// SessionStore is the slice of the authn usecase this handler needs.
type SessionStore interface {
	IssueSession(ctx context.Context, userID, userAgent string) (string, error)
	RevokeToken(ctx context.Context, tokenID string) error
}

type Deps struct {
	Logger   *logger.Logger
	Cfg      *config.ServerConfig
	Users    UserStore
	Sessions SessionStore
	// AfterSignIn, when set, runs after every successful login and
	// registration, before the answer (standalone: auto-claim the embedded
	// server into the user's org).
	AfterSignIn func(ctx context.Context, userID string)
	// Providers lists the configured sign-in providers; nil means none.
	Providers ProviderLister
}

// ProviderLister is the slice of the provider sign-in usecase State needs.
type ProviderLister interface {
	Providers() []model.SignInProvider
}

type Handler struct {
	log         *logger.Logger
	cfg         *config.ServerConfig
	users       UserStore
	sessions    SessionStore
	afterSignIn func(ctx context.Context, userID string)
	providers   ProviderLister
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, cfg: d.Cfg, users: d.Users, sessions: d.Sessions, afterSignIn: d.AfterSignIn,
		providers: d.Providers}
}

// maxUserAgent bounds the User-Agent stored with a session.
const maxUserAgent = 256

// SignIn starts a session for userID and sets its cookie on w, after
// AfterSignIn (standalone: auto-claim the embedded server). Every sign-in
// path (password, registration, a provider's callback) goes through it.
func (h *Handler) SignIn(w http.ResponseWriter, r *http.Request, userID string) error {
	ua := r.UserAgent()
	if len(ua) > maxUserAgent {
		ua = ua[:maxUserAgent]
	}
	raw, err := h.sessions.IssueSession(r.Context(), userID, ua)
	if err != nil {
		return err
	}
	if h.afterSignIn != nil {
		h.afterSignIn(r.Context(), userID)
	}
	authn.SetSessionCookie(w, raw, h.cfg.GetSecureCookies())
	return nil
}

// signIn is SignIn answering its failure: the bool reports success; on
// failure w already carries the error.
func (h *Handler) signIn(w http.ResponseWriter, r *http.Request, userID string) bool {
	if err := h.SignIn(w, r, userID); err != nil {
		h.log.Error("issue session", "error", err, "user_id", userID)
		webutil.ErrorStatus(w, http.StatusInternalServerError, "failed to start a session")
		return false
	}
	return true
}

// registrationOpen is the single policy point: claim step always open;
// otherwise standalone closed, distributed per the toggle.
func (h *Handler) registrationOpen(ctx context.Context) (open, fresh bool, err error) {
	n, err := h.users.CountUsers(ctx)
	if err != nil {
		return false, false, err
	}
	if n == 0 {
		return true, true, nil
	}
	if h.cfg.IsStandalone() {
		return false, false, nil
	}
	return h.cfg.IsRegistrationEnabled(), false, nil
}

func validEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	return at > 0 && at < len(email)-1 && !strings.ContainsAny(email, " \t")
}

// RegisterRequest is the body of POST /api/v1/auth/register.
type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RegisterResponse is the data of POST /api/v1/auth/register.
type RegisterResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

// Register creates an account and signs it in (session cookie). Fresh
// instance → claim step (owner of a new org, standalone's only org).
// Distributed → own org per registrant.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[RegisterRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if name == "" || len(name) > 64 {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	if !validEmail(email) || len(email) > 255 {
		webutil.Error(w, "a valid email is required", nil)
		return
	}
	if len(req.Password) < model.MinPasswordLen {
		webutil.Error(w, "password must be at least 4 characters", nil)
		return
	}

	open, fresh, err := h.registrationOpen(r.Context())
	if err != nil {
		webutil.Error(w, "failed to check registration state", nil)
		return
	}
	if !open {
		webutil.Error(w, "registration is disabled", nil)
		return
	}

	var user model.User
	if fresh {
		// Claim step: first account owns the (single, in standalone) org.
		// BootstrapLocalAdmin re-checks the zero-users condition inside its
		// transaction, so a racing double-submit has exactly one winner.
		user, err = h.users.BootstrapLocalAdmin(r.Context(), name, email, req.Password)
		if err == model.ErrNotBootstrap && !h.cfg.IsStandalone() && h.cfg.IsRegistrationEnabled() {
			// Lost the claim race on distributed with open registration —
			// fall through to a normal registration.
			user, err = h.users.RegisterLocalUser(r.Context(), name, email, req.Password)
		}
	} else {
		user, err = h.users.RegisterLocalUser(r.Context(), name, email, req.Password)
	}
	if err != nil {
		switch err {
		case model.ErrEmailTaken:
			webutil.Error(w, "email already in use", nil)
		case model.ErrNotBootstrap:
			webutil.Error(w, "registration is disabled", nil)
		default:
			h.log.Error("Register", "error", err, "email", email)
			webutil.Error(w, "failed to register", nil)
		}
		return
	}

	if !h.signIn(w, r, user.ID) {
		return
	}
	webutil.Success(w, "registered", RegisterResponse{UserID: user.ID, Email: email})
}

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// SessionUser is the signed-in user as the login answer carries it.
// AvatarURL is empty unless the user has an absolute https avatar.
type SessionUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// LoginResponse is the data of POST /api/v1/auth/login.
type LoginResponse struct {
	User SessionUser `json:"user"`
}

// errBadCredentials is the one answer to an unknown email and a wrong
// password alike, so the login is no oracle for which accounts exist.
const errBadCredentials = "invalid email or password"

// Login checks an email and password and starts a session (cookie).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[LoginRequest](w, r)
	if !ok {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" || req.Password == "" {
		webutil.ErrorStatus(w, http.StatusUnauthorized, errBadCredentials)
		return
	}
	user, err := h.users.VerifyLocalCredentials(r.Context(), email, req.Password)
	if errors.Is(err, model.ErrInvalidCredentials) {
		webutil.ErrorStatus(w, http.StatusUnauthorized, errBadCredentials)
		return
	}
	if err != nil {
		h.log.Error("Login", "error", err)
		webutil.ErrorStatus(w, http.StatusInternalServerError, "failed to sign in")
		return
	}
	if !h.signIn(w, r, user.ID) {
		return
	}
	webutil.Success(w, "signed in", LoginResponse{User: SessionUser{
		ID: user.ID, Name: user.Name, Email: email, AvatarURL: avatarURL(user.AvatarURL),
	}})
}

// avatarURL keeps only an absolute https avatar: the relative /avatar/...
// paths the previous auth library stored no longer resolve.
func avatarURL(u string) string {
	if strings.HasPrefix(u, "https://") {
		return u
	}
	return ""
}

// Logout ends the presenting session and clears the cookie. A request
// authenticated by another kind of token only gets the cookie cleared: a
// personal access token is revoked from the tokens page, not by a logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	p, ok := webutil.Principal(r)
	if !ok {
		webutil.Unauthorized(w)
		return
	}
	if p.Kind == model.TokenSession {
		if err := h.sessions.RevokeToken(r.Context(), p.TokenID); err != nil {
			h.log.Error("Logout", "error", err, "user_id", p.UserID)
			webutil.ErrorStatus(w, http.StatusInternalServerError, "failed to sign out")
			return
		}
	}
	authn.ClearSessionCookie(w, h.cfg.GetSecureCookies())
	webutil.Success(w, "signed out", nil)
}

// StateResponse is the data of GET /api/v1/auth/state: bootstrap is true on
// a fresh (unclaimed) instance; providers lists the configured sign-in
// providers (a button each, linking to /auth/oauth/<id>/start);
// oauth_server reports whether MCP clients can connect by OAuth.
type StateResponse struct {
	Bootstrap           bool                   `json:"bootstrap"`
	RegistrationEnabled bool                   `json:"registration_enabled"`
	Providers           []model.SignInProvider `json:"providers"`
	OAuthServer         bool                   `json:"oauth_server"`
}

// State reports what the login/register pages need: whether this is a
// fresh (unclaimed) instance, whether a registration would be accepted and
// which sign-in providers are configured.
func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	open, fresh, err := h.registrationOpen(r.Context())
	if err != nil {
		webutil.Error(w, "failed to read auth state", nil)
		return
	}
	providers := []model.SignInProvider{}
	if h.providers != nil {
		providers = append(providers, h.providers.Providers()...)
	}
	// The authorization server is not wired yet.
	webutil.Success(w, "auth state", StateResponse{
		Bootstrap: fresh, RegistrationEnabled: open, Providers: providers,
	})
}
