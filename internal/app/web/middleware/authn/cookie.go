package authn

import (
	"net/http"
	"time"

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
