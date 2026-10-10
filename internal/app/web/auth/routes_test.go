package auth

import (
	"strings"
	"testing"
)

func TestRoutesAreMountedLibraryRoutes(t *testing.T) {
	routes := Routes()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if !rt.Mounted || !rt.Raw || rt.Handler != nil {
			t.Errorf("%s: want Mounted, Raw and no handler (go-pkgz serves it)", key)
		}
		if !strings.HasPrefix(rt.Path, "/auth/") && !strings.HasPrefix(rt.Path, "/avatar/") {
			t.Errorf("%s: outside the /auth and /avatar mounts", key)
		}
	}
}
