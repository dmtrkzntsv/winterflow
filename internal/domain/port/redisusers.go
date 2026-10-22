package port

import "context"

// HubRedisUsers manages the Redis ACL user each hub connects as
// (hub:<hub_id>). Only the API holds the admin connection that can do this.
type HubRedisUsers interface {
	// Ensure creates the hub's user, or resets it with a new password, and
	// disconnects every session of its old password. It returns the
	// credentials the hub must use; the password is never logged.
	Ensure(ctx context.Context, hubID string) (username, password string, err error)
	// Delete removes the hub's user and disconnects its sessions; a user
	// that does not exist is not an error.
	Delete(ctx context.Context, hubID string) error
}
