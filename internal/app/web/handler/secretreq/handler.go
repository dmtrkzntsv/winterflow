// Package secretreq serves the secret link page (/secrets/<token>): its
// view and its submission. Both need a signed-in member of the request's
// organization; the token alone is no credential. A link of another
// organization, an unknown token and one that can no longer be submitted
// all answer the same 404.
package secretreq

import (
	"context"
	"net/http"
	"time"

	"winterflow/internal/app/web/api"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// Requests is the secret links usecase as the routes use it.
type Requests interface {
	View(ctx context.Context, userID, raw string) (secretreq.View, error)
	Submit(ctx context.Context, userID, raw string, ciphertexts map[string]string) error
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger   *logger.Logger
	Requests Requests
	// Keys reads a server's published public key; Cfg is the standalone
	// fallback (the local agent certificate).
	Keys webutil.CapabilityReader
	Cfg  *config.ServerConfig
}

// Handler serves the secret link routes.
type Handler struct {
	d Deps
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler { return &Handler{d: *d} }

// ViewQuery is the query of GET view.
type ViewQuery struct {
	Token string `query:"token" required:"true" description:"The link's token (wfx_...), the last part of /secrets/<token>."`
}

// RequestView is the data of GET view: what the page shows.
type RequestView struct {
	ServerID   string            `json:"server_id"`
	ServerName string            `json:"server_name"`
	AppID      string            `json:"app_id" description:"Empty for a new app."`
	AppName    string            `json:"app_name" description:"Empty for a new app, or an app the API does not list yet."`
	Names      []string          `json:"names" description:"The secret variables asked for, one field each."`
	Notes      map[string]string `json:"notes" description:"The agent's note per name (plain text)."`
	Message    string            `json:"message" description:"The agent's message (plain text)."`
	ExpiresAt  time.Time         `json:"expires_at"`
	PublicKey  string            `json:"public_key" description:"The server's public key the browser encrypts each value to; empty when unavailable."`
}

// SubmitRequest is the body of POST submit.
type SubmitRequest struct {
	Token  string            `json:"token" required:"true"`
	Values map[string]string `json:"values" required:"true" description:"Exactly the requested names, each ECIES-encrypted with the server's public key (base64); a plaintext is refused."`
}

// maxSubmitBytes bounds a submission's body: 32 values of at most 32 KiB.
const maxSubmitBytes = 2 << 20

// Routes is the secret links slice of the route table. Both routes are
// limited per user (g.UserLimit).
func (h *Handler) Routes(g api.Guards) []api.Route {
	errs := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/secret-request/view", Handler: h.View, Auth: api.User,
			Middleware: []api.Middleware{g.UserLimit},
			Summary:    "What a secret link asks for", Tag: "secrets",
			Description: "A link that is unknown, of another organization, submitted, used or expired answers 404.",
			Query:       ViewQuery{}, Response: RequestView{}, Errors: errs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/secret-request/submit", Handler: h.Submit, Auth: api.User,
			Middleware: []api.Middleware{g.UserLimit},
			Summary:    "Submit a secret link's values, encrypted in the browser", Tag: "secrets",
			Description: "One submission per link. The API stores only the ciphertexts until the agent's save uses them.",
			Request:     SubmitRequest{}, Errors: errs,
		},
	}
}

// View answers what the link asks for.
func (h *Handler) View(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		webutil.Error(w, "token is required", nil)
		return
	}
	v, err := h.d.Requests.View(r.Context(), userID, token)
	if err != nil {
		webutil.Fail(w, h.d.Logger, "secret request", err, "read the secret request")
		return
	}
	key, err := webutil.ServerPublicKey(r.Context(), h.d.Keys, h.d.Cfg, v.ServerID)
	if err != nil {
		h.d.Logger.Warn("secret request: no public key for the server", "server_id", v.ServerID, "error", err)
	}
	webutil.Success(w, "", RequestView{
		ServerID: v.ServerID, ServerName: v.ServerName, AppID: v.AppID, AppName: v.AppName,
		Names: v.Names, Notes: v.Notes, Message: v.Message, ExpiresAt: v.ExpiresAt, PublicKey: key,
	})
}

// Submit stores the link's ciphertexts.
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBytes)
	req, ok := webutil.DecodeBody[SubmitRequest](w, r)
	if !ok {
		return
	}
	if req.Token == "" {
		webutil.Error(w, "token is required", nil)
		return
	}
	if err := h.d.Requests.Submit(r.Context(), userID, req.Token, req.Values); err != nil {
		webutil.Fail(w, h.d.Logger, "secret request", err, "submit the secret request")
		return
	}
	webutil.Success(w, "submitted", nil)
}
