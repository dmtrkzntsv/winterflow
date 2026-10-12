// Package alerts serves /api/v1/alerts/*: the caller's organization's
// notification channels (Shoutrrr URLs stored encrypted and never
// returned), alert rules, per-target overrides, the history and the firing
// alerts. Every member of the org may do all of it; server-addressed calls
// also check that the server belongs to the caller's org.
package alerts

import (
	"context"
	"errors"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// OrgResolver resolves the caller's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// AppLookup finds an app (port.AppRepository).
type AppLookup interface {
	GetApp(ctx context.Context, appID string) (model.App, error)
}

// Sealer encrypts a channel URL (secretbox.Box).
type Sealer interface {
	Seal(plain string) (string, error)
}

// Tester sends test notifications (alerting.Notifier): to a saved channel
// (outcome recorded on it) or to a draft URL. Both return the error text,
// "" on success.
type Tester interface {
	SendTo(ctx context.Context, ch alert.Channel, title, body string) string
	SendURL(ctx context.Context, rawURL, title, body string) string
}

// Deps are the handler's collaborators (all from bootstrap.Deps). Box is
// nil without JWT_SECRET: saving and testing channels then answer 400.
type Deps struct {
	Logger  *logger.Logger
	Users   OrgResolver
	Servers webutil.ServerAccess
	Apps    AppLookup
	Alerts  port.AlertRepository
	Box     Sealer
	Tester  Tester
}

// Handler serves /api/v1/alerts/*.
type Handler struct {
	log    *logger.Logger
	users  OrgResolver
	access webutil.ServerAccess
	apps   AppLookup
	store  port.AlertRepository
	box    Sealer
	tester Tester
	now    func() time.Time
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{
		log: d.Logger, users: d.Users, access: d.Servers, apps: d.Apps,
		store: d.Alerts, box: d.Box, tester: d.Tester, now: time.Now,
	}
}

// errNoBox is the answer to channel writes without JWT_SECRET.
const errNoBox = "channel encryption is unavailable: set JWT_SECRET"

// callerOrg resolves the authenticated user and their organization,
// writing the error response itself on failure.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (userID, orgID string, ok bool) {
	userID, ok = webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		h.log.Error("alerts: resolve caller org", "error", err, "user_id", userID)
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// requireTarget authorizes a (server, app) target: the server must be the
// caller's (403) and belong to the org whose alerts are being edited (403);
// an app must exist on that server (400).
func (h *Handler) requireTarget(w http.ResponseWriter, r *http.Request, userID, orgID string, t alert.Target) bool {
	if t.ServerID == "" {
		webutil.Error(w, "server_id is required", nil)
		return false
	}
	if !webutil.RequireServerAccess(w, r, h.access, userID, t.ServerID) {
		return false
	}
	owner, err := h.store.ServerOrgID(r.Context(), t.ServerID)
	if err != nil || owner != orgID {
		webutil.Forbidden(w)
		return false
	}
	if t.AppID == "" {
		return true
	}
	a, err := h.apps.GetApp(r.Context(), t.AppID)
	if err != nil || a.ServerID != t.ServerID {
		webutil.Error(w, "unknown app", nil)
		return false
	}
	return true
}

// requireChannels checks every id names one of the org's channels.
func (h *Handler) requireChannels(w http.ResponseWriter, r *http.Request, orgID string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	chans, err := h.store.ListChannels(r.Context(), orgID)
	if err != nil {
		h.fail(w, "load channels", err)
		return false
	}
	known := make(map[string]bool, len(chans))
	for _, c := range chans {
		known[c.ID] = true
	}
	for _, id := range ids {
		if !known[id] {
			webutil.Error(w, "unknown channel "+id, nil)
			return false
		}
	}
	return true
}

// fail logs an unexpected store error and answers a generic 400; not
// found becomes its own message.
func (h *Handler) fail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, alert.ErrNotFound) {
		webutil.Error(w, what+": not found", nil)
		return
	}
	h.log.Error("alerts: "+what, "error", err)
	webutil.Error(w, "failed to "+what, nil)
}

// IDRequest addresses one channel or rule.
type IDRequest struct {
	ID string `json:"id"`
}
