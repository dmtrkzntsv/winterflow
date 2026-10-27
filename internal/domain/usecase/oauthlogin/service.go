// Package oauthlogin is sign-in with a provider (Google, Apple, a generic
// OpenID Connect issuer), Winterflow being the OAuth client, and the
// provider accounts linked to users. Ported from econumo's internal/oauth,
// with one difference: the callback signs the browser in itself (a session
// cookie) instead of handing a one-shot code to the SPA.
//
// A sign-in is bound three ways. The state sent to the provider is stored
// (hashed, single use, 10 minutes) with the nonce the ID token must carry and
// the PKCE verifier the exchange must present. The browser that started it
// keeps a flow secret in a cookie, whose hash the state holds, so a callback
// URL replayed in another browser (login CSRF: signing a victim into the
// attacker's account) is refused. A link also requires the callback to
// carry a session of the user who started it, so an attacker's link URL
// opened by a victim cannot attach the victim's provider account to the
// attacker's user.
//
// A provider account no user has linked is never attached to an existing
// account by its email: when the address already belongs to an account the
// sign-in is refused, and the owner links the provider from their settings.
// Otherwise a new account is created only while registration is open.
package oauthlogin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

const (
	// StateTTL is how long a started sign-in may take at the provider.
	StateTTL = 10 * time.Minute
	// sweepInterval throttles the expired-state sweep run by Start.
	sweepInterval = time.Minute
)

// Users is the slice of the user repository a provider sign-up needs.
type Users interface {
	// CreateProviderUser creates a passwordless user with their own
	// organization and the linked identity id; firstOnly refuses
	// (model.ErrNotBootstrap) unless the instance has no users yet.
	CreateProviderUser(ctx context.Context, name string, id model.Identity, firstOnly bool) (model.User, error)
}

// Service runs the provider sign-in flows and the linked-identity list.
type Service struct {
	providers []Provider
	byID      map[string]Provider
	repo      port.SignInRepository
	users     Users
	origin    string
	now       func() time.Time
	log       *logger.Logger

	lastSweep atomic.Int64 // unix seconds of the last expired-state sweep
}

// New builds the service. origin is the instance's public origin (the
// redirect URIs are <origin>/auth/oauth/<id>/callback); now defaults to
// time.Now; a nil log discards.
func New(providers []Provider, repo port.SignInRepository, users Users, origin string, now func() time.Time, log *logger.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = &logger.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	byID := make(map[string]Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}
	return &Service{providers: providers, byID: byID, repo: repo, users: users,
		origin: strings.TrimSuffix(origin, "/"), now: now, log: log}
}

// Providers lists the configured providers in display order.
func (s *Service) Providers() []model.SignInProvider {
	out := make([]model.SignInProvider, 0, len(s.providers))
	for _, p := range s.providers {
		out = append(out, model.SignInProvider{ID: p.ID, Name: p.Name})
	}
	return out
}

// RedirectURI is the one callback URL an operator registers per provider.
func (s *Service) RedirectURI(provider string) string {
	return s.origin + "/auth/oauth/" + provider + "/callback"
}

func (s *Service) provider(id string) (Provider, error) {
	p, ok := s.byID[id]
	if !ok {
		return Provider{}, model.ErrProviderNotConfigured
	}
	return p, nil
}

// sweepExpired purges expired states at most once a minute per process:
// starting a sign-in is anonymous, and a DELETE per click would contend with
// real writes on SQLite's single writer.
func (s *Service) sweepExpired(ctx context.Context, now time.Time) error {
	last := s.lastSweep.Load()
	if now.Unix()-last < int64(sweepInterval/time.Second) || !s.lastSweep.CompareAndSwap(last, now.Unix()) {
		return nil
	}
	return s.repo.DeleteExpiredLoginStates(ctx, now)
}

// randomToken is 32 random bytes as unpadded base64url.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pkceChallenge is RFC 7636's S256 transform.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
