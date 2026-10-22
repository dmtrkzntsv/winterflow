package port

import (
	"context"
	"errors"
	"fmt"
)

// maxHubRedisIDLen is a DNS name's length limit.
const maxHubRedisIDLen = 253

// ErrInvalidHubRedisID: the hub id has a character outside [a-z0-9.-] (it
// is used verbatim in ACL key and channel patterns, so a glob or rule
// character would widen or break them). An IPv6 address is one such id.
var ErrInvalidHubRedisID = errors.New("redis hub user: invalid hub id")

// CheckHubRedisID reports whether hubID can have a Redis user: 1-253
// characters of [a-z0-9.-]. Callers check it before anything is spent or
// recorded; every HubRedisUsers implementation refuses such an id too.
func CheckHubRedisID(hubID string) error {
	if hubID == "" || len(hubID) > maxHubRedisIDLen {
		return fmt.Errorf("%w: %q", ErrInvalidHubRedisID, hubID)
	}
	for i := 0; i < len(hubID); i++ {
		c := hubID[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return fmt.Errorf("%w: %q", ErrInvalidHubRedisID, hubID)
		}
	}
	return nil
}

// HubRedisUsers manages the Redis ACL user each hub connects as
// (hub:<hub_id>). Only the API holds the admin connection that can do this.
type HubRedisUsers interface {
	// Ensure creates the hub's user (an id CheckHubRedisID refuses is an
	// ErrInvalidHubRedisID, as in Delete), or resets it with a new password, and
	// disconnects every session of its old password. It returns the
	// credentials the hub must use; the password is never logged.
	Ensure(ctx context.Context, hubID string) (username, password string, err error)
	// Delete removes the hub's user and disconnects its sessions; a user
	// that does not exist is not an error.
	Delete(ctx context.Context, hubID string) error
}
