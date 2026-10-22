// Package hubusers manages the Redis ACL user of every hub (hub:<hub_id>):
// the API creates or resets it when a hub enrolls and deletes it when the
// hub is retired, over its own admin connection. A hub's user may write only
// its own keys, read every hub's status (its peer listing), publish and
// subscribe only where a hub does, with the few commands that takes. Every
// change is written to the server's aclfile (ACL SAVE).
package hubusers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"winterflow/internal/domain/port"
)

// callTimeout bounds each Ensure / Delete as a whole.
const callTimeout = 5 * time.Second

var (
	// ErrInvalidHubID is port.ErrInvalidHubRedisID: the id has a character
	// outside [a-z0-9.-] (port.CheckHubRedisID).
	ErrInvalidHubID = port.ErrInvalidHubRedisID
	// ErrNoACLFile: the Redis server has no aclfile, so users could not be
	// saved and would be lost on restart. A configuration error.
	ErrNoACLFile = errors.New("redis hub user: the Redis server has no aclfile configured")
)

// Users is the Redis-backed port.HubRedisUsers.
type Users struct {
	rc *redis.Client
}

var _ port.HubRedisUsers = (*Users)(nil)

// New uses rc, an admin connection (the default user with requirepass).
func New(rc *redis.Client) *Users { return &Users{rc: rc} }

// Username is the ACL user of hubID.
func Username(hubID string) string { return "hub:" + hubID }

// HubACLRules is the exact rule list of hubID's user (after "reset on
// >password"), matching what a hub does and nothing more:
//   - keys: its own status and drain keys read/write; every hub's status
//     read-only (the peer listing a stopping hub does: SCAN + MGET). SCAN
//     shows every key name, so the dedicated Redis instance must hold only
//     hub:* keys;
//   - root channels, publish only: hub:requests (to the API), bus:responses
//     and bus:events (agent replies and events);
//   - a selector, subscribe only: its own bus:requests:<id> and
//     hub:responses:<id>, and hub:broadcast (so no hub can forge a
//     broadcast or read another hub's or the API's traffic);
//   - the commands its go-redis client sends, the connection handshake and
//     SELECT (a non-zero REDIS_DB) included. Not CLIENT SETINFO: Redis 7.0
//     and 7.1 (what Debian 12 and Ubuntu 24.04 ship) refuse the whole
//     SETUSER over a subcommand they do not know, and NewRedisClient sets
//     DisableIdentity, so the client never sends it.
func HubACLRules(hubID string) []string {
	return []string{
		"resetkeys", "resetchannels",
		"~hub:status:" + hubID,
		"~hub:draining:" + hubID,
		"%R~hub:status:*",
		"&hub:requests", "&bus:responses", "&bus:events",
		"-@all",
		"+ping", "+hello", "+auth", "+select",
		"+client|setname",
		"+get", "+set", "+del", "+exists", "+scan", "+mget",
		"+publish", "+unsubscribe",
		"(&bus:requests:" + hubID + " &hub:responses:" + hubID +
			" &hub:broadcast +subscribe)",
	}
}

// Ensure creates hubID's user or resets it with a fresh password, then
// disconnects the sessions of the old one and saves the aclfile.
func (u *Users) Ensure(ctx context.Context, hubID string) (string, string, error) {
	if err := validHubID(hubID); err != nil {
		return "", "", err
	}
	password, err := newPassword()
	if err != nil {
		return "", "", fmt.Errorf("redis hub user: password: %w", err)
	}
	name := Username(hubID)
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	args := []any{"ACL", "SETUSER", name, "reset", "on", ">" + password}
	for _, r := range HubACLRules(hubID) {
		args = append(args, r)
	}
	if err := u.rc.Do(ctx, args...).Err(); err != nil {
		// Redis quotes a modifier it refuses; never let that be the password.
		if strings.Contains(err.Error(), password) {
			return "", "", fmt.Errorf("redis hub user: set %s: the server refused the password", name)
		}
		return "", "", fmt.Errorf("redis hub user: set %s: %w", name, err)
	}
	if err := u.kill(ctx, name); err != nil {
		return "", "", err
	}
	if err := u.save(ctx); err != nil {
		return "", "", err
	}
	return name, password, nil
}

// Delete removes hubID's user (ACL DELUSER also closes its connections; the
// CLIENT KILL after it covers a server that does not) and saves the aclfile.
func (u *Users) Delete(ctx context.Context, hubID string) error {
	if err := validHubID(hubID); err != nil {
		return err
	}
	name := Username(hubID)
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := u.rc.Do(ctx, "ACL", "DELUSER", name).Err(); err != nil {
		return fmt.Errorf("redis hub user: delete %s: %w", name, err)
	}
	if err := u.kill(ctx, name); err != nil {
		return err
	}
	return u.save(ctx)
}

// kill disconnects every session of user name; a missing user or no
// session is not an error.
func (u *Users) kill(ctx context.Context, name string) error {
	err := u.rc.Do(ctx, "CLIENT", "KILL", "USER", name).Err()
	if err != nil && !isNoSuchUser(err) {
		return fmt.Errorf("redis hub user: disconnect %s: %w", name, err)
	}
	return nil
}

func (u *Users) save(ctx context.Context) error {
	err := u.rc.Do(ctx, "ACL", "SAVE").Err()
	switch {
	case err == nil:
		return nil
	case isNoACLFile(err):
		return fmt.Errorf("%w: %v", ErrNoACLFile, err)
	default:
		return fmt.Errorf("redis hub user: acl save: %w", err)
	}
}

func validHubID(id string) error { return port.CheckHubRedisID(id) }

// newPassword is 32 random bytes, base64url without padding.
func newPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isNoACLFile(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "acl file") || strings.Contains(s, "aclfile")
}

func isNoSuchUser(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such user")
}
