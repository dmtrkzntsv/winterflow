package authserver

import (
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// AuthorizationQuery is the OAuth query of /oauth/authorize, which the
// consent page forwards as is.
type AuthorizationQuery struct {
	ClientID            string `query:"client_id" required:"true"`
	RedirectURI         string `query:"redirect_uri" required:"true"`
	ResponseType        string `query:"response_type" required:"true" description:"Must be code."`
	CodeChallenge       string `query:"code_challenge" required:"true" description:"PKCE S256 challenge."`
	CodeChallengeMethod string `query:"code_challenge_method" required:"true" description:"Must be S256."`
	Resource            string `query:"resource" description:"Resource indicator: this server's /mcp, or absent."`
	Scope               string `query:"scope" description:"Ignored: every grant is scope mcp."`
	State               string `query:"state" description:"Opaque client state (at most 1024 bytes), echoed back."`
}

// AuthorizationRequest is the body of approve-authorization and
// decline-authorization: the same OAuth parameters.
type AuthorizationRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource,omitempty"`
	Scope               string `json:"scope,omitempty"`
	State               string `json:"state,omitempty"`
}

func (q AuthorizationRequest) toModel() model.AuthorizationRequest {
	return model.AuthorizationRequest{
		ClientID: q.ClientID, RedirectURI: q.RedirectURI, ResponseType: q.ResponseType,
		CodeChallenge: q.CodeChallenge, CodeChallengeMethod: q.CodeChallengeMethod,
		Resource: q.Resource, Scope: q.Scope, State: q.State,
	}
}

// AuthorizationDescription is what the consent page shows. A non-empty
// error_redirect_url means the request is bad but its redirect is trusted:
// the page navigates there instead of asking.
type AuthorizationDescription struct {
	ClientName       string `json:"client_name"`
	RedirectHost     string `json:"redirect_host"`
	IsLoopback       bool   `json:"is_loopback"`
	ErrorRedirectURL string `json:"error_redirect_url"`
}

// AuthorizationDecision is where the browser goes next.
type AuthorizationDecision struct {
	RedirectURL string `json:"redirect_url"`
}

// ConnectedApp is one connected app (a live grant).
type ConnectedApp struct {
	GrantID    string    `json:"grant_id"`
	ClientName string    `json:"client_name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// RevokeConnectedAppRequest is the body of revoke-connected-app.
type RevokeConnectedAppRequest struct {
	GrantID string `json:"grant_id" required:"true"`
}

// GetAuthorizationRequest validates the consent page's OAuth parameters and
// describes the request. An untrusted client or redirect is a 400 to show
// the user; a bad parameter with a trusted redirect answers 200 with
// error_redirect_url.
func (h *Handler) GetAuthorizationRequest(w http.ResponseWriter, r *http.Request) {
	if !h.apiEnabled(w) {
		return
	}
	if _, ok := webutil.RequireUser(w, r); !ok {
		return
	}
	q := r.URL.Query()
	req := AuthorizationRequest{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"),
		CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"),
		Resource: q.Get("resource"), Scope: q.Get("scope"), State: q.Get("state"),
	}
	d, err := h.srv.DescribeAuthorization(r.Context(), req.toModel())
	if err != nil {
		webutil.Fail(w, h.log, "authserver", err, "read the authorization request")
		return
	}
	webutil.Success(w, "authorization request", AuthorizationDescription{
		ClientName: d.ClientName, RedirectHost: d.RedirectHost, IsLoopback: d.IsLoopback, ErrorRedirectURL: d.ErrorRedirectURL,
	})
}

// ApproveAuthorization issues a one-time code for the client and answers
// the URL the browser must go to.
func (h *Handler) ApproveAuthorization(w http.ResponseWriter, r *http.Request) {
	if !h.apiEnabled(w) {
		return
	}
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[AuthorizationRequest](w, r)
	if !ok {
		return
	}
	to, err := h.srv.ApproveAuthorization(r.Context(), userID, req.toModel())
	if err != nil {
		webutil.Fail(w, h.log, "authserver", err, "approve the authorization")
		return
	}
	webutil.Success(w, "authorization approved", AuthorizationDecision{RedirectURL: to})
}

// DeclineAuthorization answers the URL that sends access_denied back to the
// client.
func (h *Handler) DeclineAuthorization(w http.ResponseWriter, r *http.Request) {
	if !h.apiEnabled(w) {
		return
	}
	if _, ok := webutil.RequireUser(w, r); !ok {
		return
	}
	req, ok := webutil.DecodeBody[AuthorizationRequest](w, r)
	if !ok {
		return
	}
	to, err := h.srv.DeclineAuthorization(r.Context(), req.toModel())
	if err != nil {
		webutil.Fail(w, h.log, "authserver", err, "decline the authorization")
		return
	}
	webutil.Success(w, "authorization declined", AuthorizationDecision{RedirectURL: to})
}

// GetConnectedApps lists the caller's connected apps, oldest first.
func (h *Handler) GetConnectedApps(w http.ResponseWriter, r *http.Request) {
	if !h.apiEnabled(w) {
		return
	}
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	apps, err := h.srv.ListConnectedApps(r.Context(), userID)
	if err != nil {
		webutil.Fail(w, h.log, "authserver", err, "list connected apps")
		return
	}
	out := make([]ConnectedApp, 0, len(apps))
	for _, a := range apps {
		out = append(out, ConnectedApp{GrantID: a.GrantID, ClientName: a.ClientName,
			CreatedAt: a.CreatedAt.UTC(), LastUsedAt: a.LastUsedAt.UTC()})
	}
	webutil.Success(w, "connected apps", out)
}

// RevokeConnectedApp revokes one connected app: its grant and every access
// token issued under it stop working at once.
func (h *Handler) RevokeConnectedApp(w http.ResponseWriter, r *http.Request) {
	if !h.apiEnabled(w) {
		return
	}
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[RevokeConnectedAppRequest](w, r)
	if !ok {
		return
	}
	if !webutil.RequireUUID(w, req.GrantID, "grant_id must be a connected app id") {
		return
	}
	if err := h.srv.RevokeConnectedApp(r.Context(), userID, req.GrantID); err != nil {
		webutil.Fail(w, h.log, "authserver", err, "revoke the connected app")
		return
	}
	webutil.Success(w, "connected app revoked", nil)
}
