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
				"path", r.URL.Path,
				"client_ip", r.RemoteAddr,
			)
			next.ServeHTTP(w, r)
		})
	}
}
