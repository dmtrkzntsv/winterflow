package oauthprovider

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("oidc: unsupported curve %q", k.Crv)
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	}
	return nil, fmt.Errorf("oidc: unsupported kty %q", k.Kty)
}

// minJWKSRefresh bounds how often an unknown kid may trigger a re-fetch, so a
// flood of forged tokens (even with distinct kids) cannot turn the verifier
// into a JWKS hammer: at most one refetch per minute, per issuer, is spent
// looking for a kid that turns out not to exist. A refetch that actually
// finds the requested kid (a real rotation) never arms the throttle, so a
// genuine rotation always goes through immediately.
const minJWKSRefresh = time.Minute

// The fetched key set is trusted for the response's Cache-Control max-age,
// clamped to [minJWKSLifetime, maxJWKSLifetime] (defaultJWKSLifetime without
// one), then fetched again, so a key the issuer removed (a rotated-out or
// compromised one) stops verifying within that lifetime.
const (
	minJWKSLifetime     = 5 * time.Minute
	defaultJWKSLifetime = time.Hour
	maxJWKSLifetime     = 24 * time.Hour
)

// keyFor resolves the signing key for kid from a key set no older than its
// lifetime, re-fetching it once it expired and (throttled) on a miss. A
// failed refetch of an expired set fails the verification: an issuer
// unreachable past the lifetime must not keep its old keys trusted.
func (c *Client) keyFor(ctx context.Context, kid string) (crypto.PublicKey, error) {
	c.mu.Lock()
	now := c.now()
	fresh := c.keys != nil && now.Before(c.keysUntil)
	if fresh {
		if k, ok := c.keys[kid]; ok {
			c.mu.Unlock()
			return k, nil
		}
	}
	// A miss in a fresh set is throttled; an expired set is always refetched.
	throttled := fresh && now.Sub(c.lastMissAt) < minJWKSRefresh
	c.mu.Unlock()
	if throttled {
		return nil, fmt.Errorf("%w: unknown kid %q", ErrInvalidToken, kid)
	}
	if err := c.fetchJWKS(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	c.lastMissAt = c.now()
	return nil, fmt.Errorf("%w: unknown kid %q", ErrInvalidToken, kid)
}

// jwksLifetime reads max-age from a Cache-Control header, clamped; no-store,
// no-cache or max-age=0 take the minimum, an absent or unreadable one the
// default.
func jwksLifetime(cacheControl string) time.Duration {
	life := defaultJWKSLifetime
	for _, d := range strings.Split(cacheControl, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		switch {
		case d == "no-store" || d == "no-cache":
			return minJWKSLifetime
		case strings.HasPrefix(d, "max-age="):
			n, err := strconv.ParseInt(strings.Trim(strings.TrimPrefix(d, "max-age="), `"`), 10, 64)
			if err != nil || n < 0 {
				continue
			}
			if n > int64(maxJWKSLifetime/time.Second) {
				life = maxJWKSLifetime
			} else {
				life = time.Duration(n) * time.Second
			}
		}
	}
	return min(max(life, minJWKSLifetime), maxJWKSLifetime)
}

func (c *Client) fetchJWKS(ctx context.Context) error {
	disc, err := c.Discover(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, disc.JWKSURI, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: jwks returned status %d", resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return err
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, perr := k.publicKey()
		if perr != nil {
			continue // one exotic key must not poison the set
		}
		keys[k.Kid] = pub
	}
	life := jwksLifetime(resp.Header.Get("Cache-Control"))
	c.mu.Lock()
	c.keys = keys
	c.keysUntil = c.now().Add(life)
	c.mu.Unlock()
	return nil
}
