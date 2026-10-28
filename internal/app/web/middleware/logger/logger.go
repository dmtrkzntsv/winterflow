package logger

import (
	"net/http"
	applog "winterflow/pkg/logger"
)

func WithLogger(l *applog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			l.Debug("http_request",
				"method", r.Method,
				"path", LogPath(r.URL.Path),
				"client_ip", r.RemoteAddr,
			)
			next.ServeHTTP(w, r)
		})
	}
}

// secretsPrefix is the secret link page's path; what follows it is the
// link's token.
const secretsPrefix = "/secrets/"

// LogPath is path as the request log records it: a secret link's token
// (/secrets/<token>) is redacted.
func LogPath(path string) string {
	// (No strings import: the route golden names this file's closure by
	// its line.)
	if len(path) >= len(secretsPrefix) && path[:len(secretsPrefix)] == secretsPrefix {
		return secretsPrefix + "[redacted]"
	}
	return path
}
