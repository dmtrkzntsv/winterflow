package notification

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"winterflow/internal/app/web/middleware/authn"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// A stream is authenticated by the middleware once, at connect. While it is
// open the handler re-authenticates the same credential every
// reauthInterval and ends the stream once it no longer authenticates (a
// logout, logout-all, revoked or expired session), so a revoked session
// stops receiving events within this bound. A live stream is never closed
// on purpose: a reconnect could drop a command result published in its gap.
const (
	reauthInterval = time.Minute
	// reauthTimeout bounds one re-authentication.
	reauthTimeout = 5 * time.Second
)

type Handler struct {
	log  *logger.Logger
	nm   port.NotificationManager
	auth authn.Authenticator
	// every is the re-authentication interval (reauthInterval; tests
	// shorten it).
	every time.Duration
}

type Deps struct {
	Logger              *logger.Logger
	NotificationManager port.NotificationManager
	// Auth re-authenticates an open stream's credential (the authn
	// usecase). Without it a stream ends at its first check (fail closed).
	Auth authn.Authenticator
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		log:   d.Logger,
		nm:    d.NotificationManager,
		auth:  d.Auth,
		every: reauthInterval,
	}
}

func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	p, ok := webutil.Principal(r)
	if !ok || p.UserID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	userID := p.UserID
	// The credential the middleware authenticated, kept to check it again.
	// Never logged.
	raw, _ := authn.Credential(r)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := h.nm.AddChannel(userID)
	defer h.nm.RemoveChannel(userID, ch)

	// Send the headers now: once the client sees 200 it is subscribed, so
	// a command it dispatches next cannot finish before its result has a
	// channel to land in.
	flusher.Flush()

	ticker := time.NewTicker(h.every)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ticker.C:
			if !h.stillValid(ctx, raw, userID, p.TokenID) {
				return
			}
		case msg := <-ch:
			h.log.Debug("sending notification", "userID", userID)
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// stillValid re-authenticates the stream's credential: it must still resolve
// to the same token of the same user. Authenticate bumps last_used_at only
// as it does for any request (throttled), so the check adds no writes of
// its own. Any failure, a database error included, ends the stream; the
// browser reconnects through the middleware, which decides afresh.
func (h *Handler) stillValid(ctx context.Context, raw, userID, tokenID string) bool {
	if h.auth == nil || raw == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, reauthTimeout)
	defer cancel()
	p, err := h.auth.Authenticate(ctx, raw)
	if err != nil {
		h.log.Debug("sse: credential no longer authenticates", "userID", userID, "error", err)
		return false
	}
	return p.UserID == userID && p.TokenID == tokenID
}
