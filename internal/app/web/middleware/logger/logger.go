package logger

import (
	"net/http"
	"time"
	applog "winterflow/pkg/logger"
)

func WithLogger(l *applog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			duration := time.Since(start)
			l.Debug("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"duration_ms", duration.Milliseconds(),
			)
		})
	}
}
