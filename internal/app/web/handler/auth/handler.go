// Package auth serves the public registration surface: POST register and
// GET state. Registration is the only way accounts self-create; login is
// verify-only. Policy: a fresh instance (zero users) always accepts the
// first registration (the "claim step" — owner of a new org); afterwards
// standalone is closed (single-org: admins provision accounts at
// /org/members) and distributed follows REGISTRATION_ENABLED (own org per
// registrant).
package auth

import (
	"context"
	"net/http"
	"strings"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// RegistrationStore is the slice of port.UserService this handler needs.
type RegistrationStore interface {
	CountUsers(ctx context.Context) (int, error)
	BootstrapLocalAdmin(ctx context.Context, name, email, password string) (model.User, error)
	RegisterLocalUser(ctx context.Context, name, email, password string) (model.User, error)
}

type Deps struct {
	Logger *logger.Logger
	Cfg    *config.ServerConfig
	Users  RegistrationStore
}

type Handler struct {
	log   *logger.Logger
	cfg   *config.ServerConfig
	users RegistrationStore
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, cfg: d.Cfg, users: d.Users}
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

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates an account. Fresh instance → claim step (owner of a new
// org, standalone's only org). Distributed → own org per registrant. Never
// issues a session: the client logs in afterwards.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[registerRequest](w, r)
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

	webutil.Success(w, "registered", struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
	}{user.ID, email})
}

// State reports what the login/register pages need: whether this is a
// fresh (unclaimed) instance and whether a registration would be accepted.
func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	open, fresh, err := h.registrationOpen(r.Context())
	if err != nil {
		webutil.Error(w, "failed to read auth state", nil)
		return
	}
	webutil.Success(w, "auth state", struct {
		Bootstrap           bool `json:"bootstrap"`
		RegistrationEnabled bool `json:"registration_enabled"`
	}{Bootstrap: fresh, RegistrationEnabled: open})
}
