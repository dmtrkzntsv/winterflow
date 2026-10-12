package notification

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/alert"
	"winterflow/internal/domain/command"
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
	if !strings.Contains(rt.Description, "BEFORE dispatching") || !strings.Contains(rt.Description, "not buffered or replayed") {
		t.Errorf("Description = %q, want the delivery semantics", rt.Description)
	}
	want := map[string]any{
		"operation_result": nil,
		"server_status":    model.ServerStatusPayload{},
		"apps_status":      command.AppsStatusPayload{},
		"alert":            alert.Notice{},
	}
	if len(rt.EventPayloads) != len(want) {
		t.Fatalf("EventPayloads = %+v, want %d entries", rt.EventPayloads, len(want))
	}
	for _, ep := range rt.EventPayloads {
		if w, ok := want[ep.Type]; !ok || reflect.TypeOf(w) != reflect.TypeOf(ep.Payload) {
			t.Errorf("EventPayload %s = %#v, want %#v", ep.Type, ep.Payload, w)
		}
	}
}
