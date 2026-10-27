// Package oauthlogin serves sign-in with a provider: the browser routes that
// start a sign-in (or a link) and receive the provider's answer, and the
// linked-identity routes of the user settings.
//
// The browser routes sit outside the auth middleware: start and the GET
// callback are top-level navigations, and Apple's form_post callback is a
// cross-site POST by design, which no CSRF header can accompany; the stored
// state and the flow cookie protect them instead. The POST callback only
// re-sends its fields to the GET callback (303): the flow and session
// cookies are SameSite=Lax, so a browser sends them on that top-level GET
// but not on the cross-site POST.
package oauthlogin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/oauthlogin"
	"winterflow/pkg/logger"
)

// FlowCookie keeps the flow secret of the sign-in this browser started.
const FlowCookie = "wf_oauth_flow"

// flowCookiePath scopes the flow cookie to the sign-in routes.
const flowCookiePath = "/auth/oauth/"

// maxFormBytes bounds Apple's form_post body.
const maxFormBytes = 64 << 10

// SignIns is the provider sign-in usecase.
type SignIns interface {
	Start(ctx context.Context, req oauthlogin.StartRequest) (oauthlogin.StartResult, error)
	Callback(ctx context.Context, provider string, in oauthlogin.CallbackInput) oauthlogin.CallbackResult
	Identities(ctx context.Context, userID string) ([]model.Identity, error)
	Unlink(ctx context.Context, userID, provider string) error
}

// Sessions starts a session on the browser (the auth handler's SignIn, so
// every sign-in also auto-claims in standalone).
type Sessions interface {
	SignIn(w http.ResponseWriter, r *http.Request, userID string) error
}

type Deps struct {
	Logger   *logger.Logger
	SignIns  SignIns
	Sessions Sessions
	// Auth resolves the session cookie of a link's start and callback.
	Auth authn.Authenticator
	// Registration says whether a sign-in may create an account.
	Registration  oauthlogin.RegistrationPolicy
	SecureCookies bool
}

type Handler struct {
	log           *logger.Logger
	signIns       SignIns
	sessions      Sessions
	auth          authn.Authenticator
	registration  oauthlogin.RegistrationPolicy
	secureCookies bool
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, signIns: d.SignIns, sessions: d.Sessions, auth: d.Auth,
		registration: d.Registration, secureCookies: d.SecureCookies}
}

// StartQuery is the query of GET /auth/oauth/{provider}/start.
type StartQuery struct {
	Provider string `path:"provider" enum:"google,apple,oidc"`
	// Mode is login (default) or link (attach the provider account to the
	// signed-in user; needs the session cookie).
	Mode string `query:"mode" enum:"login,link"`
	// ReturnTo is the path on this origin to land on afterwards (default /;
	// anything else than a same-origin path is replaced by /).
	ReturnTo string `query:"return_to"`
}

// Start sends the browser to the provider.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := oauthlogin.StartRequest{Provider: chi.URLParam(r, "provider"), Intent: q.Get("mode"), ReturnTo: q.Get("return_to")}
	switch req.Intent {
	case "":
		req.Intent = model.IntentLogin
	case model.IntentLogin:
	case model.IntentLink:
		userID, ok := authn.SessionUser(r, h.auth)
		if !ok {
			webutil.Unauthorized(w)
			return
		}
		req.LinkUserID = userID
	default:
		webutil.Error(w, "mode must be login or link", nil)
		return
	}
	res, err := h.signIns.Start(r.Context(), req)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			webutil.ErrorStatus(w, http.StatusNotFound, err.Error())
			return
		}
		h.log.Error("oauth start", "error", err, "provider", req.Provider)
		webutil.ErrorStatus(w, http.StatusInternalServerError, "failed to start the sign-in")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: FlowCookie, Value: res.Flow, Path: flowCookiePath, MaxAge: int(oauthlogin.StateTTL / time.Second),
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
	noStore(w)
	http.Redirect(w, r, res.URL, http.StatusFound)
}

// CallbackQuery is the query of GET /auth/oauth/{provider}/callback: the
// provider's answer (or the fields the POST callback re-sent).
type CallbackQuery struct {
	Provider string `path:"provider" enum:"google,apple,oidc"`
	Code     string `query:"code"`
	State    string `query:"state" required:"true"`
	Error    string `query:"error"`
	// User is Apple's one-time JSON user field (the name), first sign-in only.
	User string `query:"user"`
}

// CallbackPath is the POST callback's path parameter.
type CallbackPath struct {
	Provider string `path:"provider" enum:"google,apple,oidc"`
}

// CallbackForm is Apple's form_post body.
type CallbackForm struct {
	Code  string `formData:"code"`
	State string `formData:"state" required:"true"`
	Error string `formData:"error"`
	User  string `formData:"user"`
}

// Callback receives the provider's answer, signs the browser in (or links
// the account) and sends it on.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("state") == "" {
		webutil.Error(w, "state is required", nil)
		return
	}
	in := oauthlogin.CallbackInput{
		Code: q.Get("code"), State: q.Get("state"), Error: q.Get("error"), AppleUser: q.Get("user"),
		Registration: h.registration,
	}
	if c, err := r.Cookie(FlowCookie); err == nil {
		in.Flow = c.Value
	}
	if userID, ok := authn.SessionUser(r, h.auth); ok {
		in.SessionUserID = userID
	}
	res := h.signIns.Callback(r.Context(), chi.URLParam(r, "provider"), in)
	http.SetCookie(w, &http.Cookie{
		Name: FlowCookie, Value: "", Path: flowCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
	target := res.Redirect
	if res.SignIn != "" {
		if err := h.sessions.SignIn(w, r, res.SignIn); err != nil {
			h.log.Error("oauth callback: issue session", "error", err, "user_id", res.SignIn)
			target = "/login?error=" + oauthlogin.ErrCodeProvider
		}
	}
	noStore(w)
	http.Redirect(w, r, target, http.StatusFound)
}

// CallbackPost re-sends a form_post answer to the GET callback, where the
// browser's SameSite=Lax cookies come along.
func (h *Handler) CallbackPost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		webutil.Error(w, "invalid form", nil)
		return
	}
	if r.PostForm.Get("state") == "" {
		webutil.Error(w, "state is required", nil)
		return
	}
	fwd := url.Values{}
	for _, k := range []string{"code", "state", "error", "user"} {
		if v := r.PostForm.Get(k); v != "" {
			fwd.Set(k, v)
		}
	}
	noStore(w)
	http.Redirect(w, r, r.URL.Path+"?"+fwd.Encode(), http.StatusSeeOther)
}

// noStore keeps the redirects (which may carry a provider code) out of
// caches and Referer headers.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// IdentityItem is one linked provider account.
type IdentityItem struct {
	Provider  string    `json:"provider" enum:"google,apple,oidc"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// GetIdentities lists the caller's linked provider accounts.
func (h *Handler) GetIdentities(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	ids, err := h.signIns.Identities(r.Context(), userID)
	if err != nil {
		h.log.Error("get identities", "error", err)
		webutil.Error(w, "failed to list linked accounts", nil)
		return
	}
	out := make([]IdentityItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, IdentityItem{Provider: id.Provider, Email: id.Email, CreatedAt: id.CreatedAt.UTC()})
	}
	webutil.Success(w, "linked accounts", out)
}

// UnlinkIdentityRequest is the body of POST /api/v1/user/unlink-identity.
type UnlinkIdentityRequest struct {
	Provider string `json:"provider" enum:"google,apple,oidc"`
}

// UnlinkIdentity removes one of the caller's linked provider accounts.
func (h *Handler) UnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[UnlinkIdentityRequest](w, r)
	if !ok {
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		webutil.Error(w, "provider is required", nil)
		return
	}
	if err := h.signIns.Unlink(r.Context(), userID, provider); err != nil {
		webutil.Fail(w, h.log, "unlink identity", err, "unlink the account")
		return
	}
	webutil.Success(w, "unlinked", nil)
}
