package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"winterflow/pkg/logger"
)

type fakeHubUsersChecker struct{ err error }

func (f fakeHubUsersChecker) Check(context.Context) error { return f.err }

// A Redis connection that cannot manage the hubs' users is logged as an
// error at startup (not fatal); one that can logs nothing.
func TestCheckHubUsersAdmin(t *testing.T) {
	var out bytes.Buffer
	log := &logger.Logger{Logger: slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	checkHubUsersAdmin(t.Context(), log, fakeHubUsersChecker{})
	if out.Len() != 0 {
		t.Fatalf("admin connection logged: %s", out.String())
	}
	checkHubUsersAdmin(t.Context(), log, fakeHubUsersChecker{err: errors.New("user hub:x cannot manage the hubs' users")})
	got := out.String()
	if !strings.Contains(got, `"level":"ERROR"`) || !strings.Contains(got, "hub joins will fail") || !strings.Contains(got, "user hub:x cannot manage") {
		t.Fatalf("log = %s", got)
	}
}
