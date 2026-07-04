// Package rbac enforces the admin/member split: owners and admins may
// administer the organization (members, servers, infrastructure settings);
// members keep the full app lifecycle. The role is read from the DB on every
// request so demotions apply without re-login.
package rbac

import (
	"context"
	"encoding/json"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

// RoleSource is the slice of port.UserService this middleware needs.
type RoleSource interface {
	RoleOf(ctx context.Context, userID string) (string, error)
}

// RequireAdmin passes owners and admins; everyone else gets 403.
func RequireAdmin(roles RoleSource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, err := webutil.GetUserID(r)
			if err != nil || userID == "" {
				webutil.Unauthorized(w)
				return
			}
			role, err := roles.RoleOf(r.Context(), userID)
			if err != nil || !model.OrganizationRole(role).IsAdmin() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(webutil.APIResponse{
					Success: false,
					Message: "admin role required",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
