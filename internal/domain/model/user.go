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
	// ErrInvalidCredentials is returned for a wrong email/password pair.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrLastOwner guards demoting/removing an organization's only owner.
	ErrLastOwner = errors.New("cannot remove the last owner")
	// ErrEmailTaken is returned when local credentials already use the email.
	ErrEmailTaken = errors.New("email already in use")
	// ErrNotBootstrap is returned when a bootstrap is attempted on a
	// non-empty instance (users already exist).
	ErrNotBootstrap = errors.New("users already exist")
)

// MinPasswordLen is the minimum accepted password length (registration,
// change-password, admin resets keep their generated 16-char temps).
const MinPasswordLen = 4

// Credentials is the non-secret slice of a user's local login (the hash
// never leaves the repository layer).
type Credentials struct {
	Email              string `json:"email"`
	MustChangePassword bool   `json:"must_change_password"`
}

// Member is an organization membership row joined with its user, as shown
// on the members page. Email is empty for users without local credentials.
type Member struct {
	User
	Email    string `json:"email"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
}

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
