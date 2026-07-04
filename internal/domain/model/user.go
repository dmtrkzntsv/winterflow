package model

import (
	"errors"
	"time"
)

type User struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	AvatarURL  string    `json:"avatar_url"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

var (
	ErrorUserNotFound = errors.New("user not found")
	// ErrInvalidToken is returned for an unknown or expired access token.
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenNotFound is returned when a token id does not exist for the user.
	ErrTokenNotFound = errors.New("token not found")
)

// UserToken is a personal access token record. The plaintext is never stored;
// Prefix (e.g. "wfp_9k3Ldx2p") identifies the token in lists.
type UserToken struct {
	ID         string     `json:"token_id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}
