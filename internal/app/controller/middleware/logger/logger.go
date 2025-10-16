package logger

import (
	"log"
	"net/http"
)

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		//ctx.Value()
		//log.Printf("[%s] %s %s (%s/%s) %d %v",
		//    reqID,
		//    r.Method,
		//    r.URL.Path,
		//    module,
		//    action,
		//    rw.status,
		//    duration,
		//)
	})
}
