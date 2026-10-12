package util

import (
	"context"
	"net/http"

	"winterflow/pkg/logger"
)

// OrgResolver resolves a user's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// CallerOrg resolves the caller and their org, writing 401 (no user) or
// 400 (no org) itself; an unwired resolver also answers 400. The bool
// reports success.
func CallerOrg(w http.ResponseWriter, r *http.Request, users OrgResolver, log *logger.Logger) (userID, orgID string, ok bool) {
	userID, ok = RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	if users == nil {
		Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	orgID, err := users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		log.Error("resolve caller org", "error", err, "user_id", userID)
		Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// RequireOwnedServer requires serverID (400 when empty) owned by one of userID's
// organizations (403), writing the error itself.
func RequireOwnedServer(w http.ResponseWriter, r *http.Request, access ServerAccess, userID, serverID string) bool {
	if serverID == "" {
		Error(w, "server_id is required", nil)
		return false
	}
	return RequireServerAccess(w, r, access, userID, serverID)
}
