package util

import (
	"context"
	"encoding/json"
	"net/http"
)

// ServerAccess answers whether a user may act on a server: the server must
// belong to one of the user's organizations. port.ServerRepository satisfies
// it; handlers depend on this narrow slice so tests can fake it in one line.
type ServerAccess interface {
	UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error)
}

// Forbidden writes a 403 JSON error: authenticated, but not allowed to act on
// the target resource.
func Forbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Message: "forbidden",
	})
}

// RequireServerAccess enforces that serverID belongs to one of the caller's
// organizations, writing a 403 itself when it does not. It fails closed: a
// lookup error or an unwired access checker also denies. The bool reports
// success.
func RequireServerAccess(w http.ResponseWriter, r *http.Request, access ServerAccess, userID, serverID string) bool {
	if access == nil {
		Forbidden(w)
		return false
	}
	owns, err := access.UserOwnsServer(r.Context(), userID, serverID)
	if err != nil || !owns {
		Forbidden(w)
		return false
	}
	return true
}
