package util

import (
	"encoding/json"
	"net/http"
)

// Unauthorized writes a 401 JSON error. Authentication failures are not
// client mistakes (400) — they must surface as 401 so browsers/proxies treat
// them correctly.
func Unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Message: "unauthorized",
	})
}

// RequireUser resolves the authenticated user id, writing a 401 itself when
// the request carries no valid identity. The bool reports success.
func RequireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := GetUserID(r)
	if err != nil || userID == "" {
		Unauthorized(w)
		return "", false
	}
	return userID, true
}

// DecodeBody parses the JSON request body into T, writing a 400 itself on
// malformed input. The bool reports success. Field-level validation stays
// with the caller.
func DecodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		Error(w, "invalid request body", nil)
		var zero T
		return zero, false
	}
	return v, true
}
