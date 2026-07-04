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

// TokenStore is the slice of port.UserService this handler needs.
type TokenStore interface {
	CreateToken(ctx context.Context, userID, name string, expiresAt *time.Time) (model.UserToken, string, error)
	ListTokens(ctx context.Context, userID string) ([]model.UserToken, error)
	DeleteToken(ctx context.Context, userID, tokenID string) error
}

type Deps struct {
	Logger *logger.Logger
	Tokens TokenStore
}

type Handler struct {
	log    *logger.Logger
	tokens TokenStore
}

func NewHandler(d *Deps) *Handler {
	return &Handler{log: d.Logger, tokens: d.Tokens}
}

type createTokenRequest struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

// CreateToken mints a PAT. The response carries the plaintext — the only
// time it is ever returned; the DB keeps just the hash.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[createTokenRequest](w, r)
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
	rec, plaintext, err := h.tokens.CreateToken(r.Context(), userID, name, expiresAt)
	if err != nil {
		h.log.Error("CreateToken", "error", err, "user_id", userID)
		webutil.Error(w, "failed to create token", nil)
		return
	}
	webutil.Success(w, "token created", struct {
		TokenID   string     `json:"token_id"`
		Token     string     `json:"token"`
		Prefix    string     `json:"prefix"`
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}{rec.ID, plaintext, rec.Prefix, rec.Name, rec.ExpiresAt})
}

// GetTokens lists the caller's tokens — never any plaintext.
func (h *Handler) GetTokens(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	list, err := h.tokens.ListTokens(r.Context(), userID)
	if err != nil {
		h.log.Error("GetTokens", "error", err, "user_id", userID)
		webutil.Error(w, "failed to list tokens", nil)
		return
	}
	webutil.Success(w, "tokens", list)
}

type deleteTokenRequest struct {
	TokenID string `json:"token_id"`
}

// DeleteToken revokes one of the caller's tokens. Unknown ids (including
// other users' tokens) report not-found.
func (h *Handler) DeleteToken(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[deleteTokenRequest](w, r)
	if !ok {
		return
	}
	if req.TokenID == "" {
		webutil.Error(w, "token_id is required", nil)
		return
	}
	if err := h.tokens.DeleteToken(r.Context(), userID, req.TokenID); err != nil {
		webutil.Error(w, "token not found", nil)
		return
	}
	webutil.Success(w, "token deleted", nil)
}
