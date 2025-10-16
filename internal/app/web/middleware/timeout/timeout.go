package timeout

import (
	"net/http"
	"time"
)

func WithTimeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if d <= 0 {
			return next
		}
		return http.TimeoutHandler(next, d, "request timed out")
	}
}
