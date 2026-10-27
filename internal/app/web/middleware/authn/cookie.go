package authn

import (
	"net/http"
	"time"

	"winterflow/internal/domain/model"
	authnuc "winterflow/internal/domain/usecase/authn"
)

// cookieMaxAge is how long the browser keeps the session cookie: a
// session's longest life. The server decides when the session itself ends.
const cookieMaxAge = authnuc.SessionMaxAge

// SetSessionCookie hands the browser its session token: HttpOnly,
// SameSite=Lax, Path /, Secure when the instance is served over https.
func SetSessionCookie(w http.ResponseWriter, raw string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: raw, Path: "/",
		MaxAge: int(cookieMaxAge / time.Second), HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie tells the browser to drop the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// SessionUser resolves the request's session cookie alone (no Bearer or
// Basic), for the browser routes outside the auth middleware that need to
// know who is signed in on this browser: the user id, or false when the
// cookie is absent or does not authenticate a session.
func SessionUser(r *http.Request, a Authenticator) (string, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	p, err := a.Authenticate(r.Context(), c.Value)
	if err != nil || p.Kind != model.TokenSession {
		return "", false
	}
	return p.UserID, true
}
