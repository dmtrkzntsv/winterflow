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

// Token is personal access token metadata (without the secret).
type Token struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

var (
	ErrorUserNotFound = errors.New("user not found")
	// ErrInvalidToken is returned for an unknown or expired access token.
	ErrInvalidToken = errors.New("invalid token")
)
