package notification

import (
	"net/http"
	"testing"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	rt := routes[0]
	if rt.Method != http.MethodGet || rt.Path != "/api/v1/sse" || rt.Handler == nil || rt.Auth != api.User {
		t.Errorf("sse route = %+v", rt)
	}
	if _, ok := rt.Events.(model.Notification); !ok {
		t.Errorf("Events = %T, want model.Notification", rt.Events)
	}
}
