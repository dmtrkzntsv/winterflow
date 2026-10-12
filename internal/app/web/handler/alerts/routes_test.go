package alerts

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func limitGuard(next http.Handler) http.Handler { return next }

// test-channel makes the API send outbound traffic on demand (up to the
// send timeout each), so it sits behind the per-IP limiter; nothing else
// in the alerts table does.
func TestRoutesLimitTestChannel(t *testing.T) {
	limited := map[string]bool{}
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{AuthLimit: limitGuard}) {
		for _, mw := range rt.Middleware {
			if reflect.ValueOf(mw).Pointer() == reflect.ValueOf(limitGuard).Pointer() {
				limited[rt.Method+" "+rt.Path] = true
			}
		}
	}
	if want := map[string]bool{"POST /api/v1/alerts/test-channel": true}; !reflect.DeepEqual(limited, want) {
		t.Fatalf("limited routes = %v, want %v", limited, want)
	}
}
