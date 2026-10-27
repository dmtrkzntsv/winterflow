// Package user serves user-scoped endpoints: personal access token
// management. All three are synchronous DB operations (no agent round-trip).
package user

import (
	"context"
	"net/http"
	"strings"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

// TokenStore is the slice of the authn usecase this handler needs.
type TokenStore interface {
	CreatePAT(ctx context.Context, userID, name string, expiresAt *time.Time) (model.AccessToken, string, error)
	ListPATs(ctx context.Context, userID string) ([]model.AccessToken, error)
	DeletePAT(ctx context.Context, userID, tokenID string) error
}

// ProfileStore is the slice of port.UserService the profile and password
// endpoints need.
type ProfileStore interface {
	GetUser(ctx context.Context, userID string) (model.User, error)
	GetCredentials(ctx context.Context, userID string) (model.Credentials, error)
	VerifyLocalCredentials(ctx context.Context, email, password string) (model.User, error)
	SetPassword(ctx context.Context, userID, password string, mustChange bool) error
}

type Deps struct {
	Logger *logger.Logger
	Tokens TokenStore
	Users  ProfileStore
}

type Handler struct {
	log    *logger.Logger
	tokens TokenStore
	users  ProfileStore
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, tokens: d.Tokens, users: d.Users}
}

// Profile is the data of GET /api/v1/user/get-profile. Email and
// must_change_password are empty/false for accounts without local credentials.
type Profile struct {
	UserID             string `json:"user_id"`
	Name               string `json:"name"`
	Email              string `json:"email"`
	MustChangePassword bool   `json:"must_change_password"`
}

// GetProfile returns the caller's identity as the UI needs it: id, name and,
// for local accounts, email + the must-change flag (drives the forced
// password-change redirect).
func (h *Handler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	u, err := h.users.GetUser(r.Context(), userID)
	if err != nil {
		webutil.Error(w, "failed to load profile", nil)
		return
	}
	profile := Profile{UserID: u.ID, Name: u.Name}
	if creds, err := h.users.GetCredentials(r.Context(), userID); err == nil {
		profile.Email = creds.Email
		profile.MustChangePassword = creds.MustChangePassword
	}
	webutil.Success(w, "profile", profile)
}

// ChangePasswordRequest is the body of POST /api/v1/user/change-password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword verifies the current password and stores a new bcrypt hash,
// clearing the must-change flag. Users without local credentials get a 400.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[ChangePasswordRequest](w, r)
	if !ok {
		return
	}
	if len(req.NewPassword) < model.MinPasswordLen {
		webutil.Error(w, "new password must be at least 4 characters", nil)
		return
	}
	creds, err := h.users.GetCredentials(r.Context(), userID)
	if err != nil {
		webutil.Error(w, "account has no local password", nil)
		return
	}
	if _, err := h.users.VerifyLocalCredentials(r.Context(), creds.Email, req.CurrentPassword); err != nil {
		webutil.Error(w, "current password is incorrect", nil)
		return
	}
	if err := h.users.SetPassword(r.Context(), userID, req.NewPassword, false); err != nil {
		h.log.Error("ChangePassword", "error", err, "user_id", userID)
		webutil.Error(w, "failed to change password", nil)
		return
	}
	webutil.Success(w, "password changed", nil)
}

// CreateTokenRequest is the body of POST /api/v1/user/create-token.
// expires_in_days <= 0 means the token never expires.
type CreateTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// CreateTokenResponse is the data of POST /api/v1/user/create-token. Token is
// the plaintext PAT, returned only here.
type CreateTokenResponse struct {
	TokenID   string     `json:"token_id"`
	Token     string     `json:"token"`
	Prefix    string     `json:"prefix"`
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// CreateToken mints a PAT. The response carries the plaintext — the only
// time it is ever returned; the DB keeps just the hash.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[CreateTokenRequest](w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		webutil.Error(w, "name is required (max 64 chars)", nil)
		return
	}
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		t := time.Now().AddDate(0, 0, req.ExpiresInDays)
		expiresAt = &t
	}
	rec, plaintext, err := h.tokens.CreatePAT(r.Context(), userID, name, expiresAt)
	if err != nil {
		h.log.Error("CreateToken", "error", err, "user_id", userID)
		webutil.Error(w, "failed to create token", nil)
		return
	}
	webutil.Success(w, "token created", CreateTokenResponse{
		TokenID:   rec.ID,
		Token:     plaintext,
		Prefix:    rec.Prefix,
		Name:      rec.Name,
		ExpiresAt: rec.ExpiresAt,
	})
}

// GetTokens lists the caller's tokens — never any plaintext.
func (h *Handler) GetTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	list, err := h.tokens.ListPATs(r.Context(), userID)
	if err != nil {
		h.log.Error("GetTokens", "error", err, "user_id", userID)
		webutil.Error(w, "failed to list tokens", nil)
		return
	}
	out := make([]model.UserToken, 0, len(list))
	for _, t := range list {
		out = append(out, tokenView(t))
	}
	webutil.Success(w, "tokens", out)
}

// tokenView is a PAT as the token list shows it (last_used_at null when it
// was never used).
func tokenView(t model.AccessToken) model.UserToken {
	v := model.UserToken{
		ID: t.ID, UserID: t.UserID, Name: t.Name, Prefix: t.Prefix,
		ExpiresAt: t.ExpiresAt, CreatedAt: t.CreatedAt,
	}
	if !t.LastUsedAt.IsZero() {
		last := t.LastUsedAt
		v.LastUsedAt = &last
	}
	return v
}

// DeleteTokenRequest is the body of POST /api/v1/user/delete-token.
type DeleteTokenRequest struct {
	TokenID string `json:"token_id"`
}

// DeleteToken revokes one of the caller's tokens. Unknown ids (including
// other users' tokens) report not-found.
func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteTokenRequest](w, r)
	if !ok {
		return
	}
	if req.TokenID == "" {
		webutil.Error(w, "token_id is required", nil)
		return
	}
	if err := h.tokens.DeletePAT(r.Context(), userID, req.TokenID); err != nil {
		webutil.Error(w, "token not found", nil)
		return
	}
	webutil.Success(w, "token deleted", nil)
}
