package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestRestoreAndOrphanRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)

	t.Run("restore-backup answers when the restore ends", func(t *testing.T) {
		var res command.RestoreBackupResponse
		owner.succeed(t, post("/api/v1/app/restore-backup", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), &res)
		if res.RestoredPoint != "20261011T120000Z-scheduled" || res.PreRestorePoint != "20261011T140000Z-pre-restore" {
			t.Fatalf("restore = %+v", res)
		}
		got := e.agent.seen(command.TypeBackupRestore)
		if in := decode[command.RestoreBackupRequest](got[len(got)-1].Payload); in.AppID != anApp || in.Backup != "database" {
			t.Fatalf("agent got %+v", in)
		}
		const partial = "the restore stopped half-way: /app/media was partly replaced. The app was left stopped; " +
			"restore the pre-restore point 20261011T140000Z-pre-restore to go back"
		e.agent.fail(command.TypeBackupRestore, partial)
		owner.failWith(t, post("/api/v1/app/restore-backup", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), partial)
		n := e.agent.count()
		for body, want := range map[string]string{
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backup":"database","point":"latest"}`:            "invalid restore point id",
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backup":"DB","point":"20261011T120000Z-manual"}`: "is not a valid backup name",
			`{"app_id":"` + anApp + `","backup":"database","point":"20261011T120000Z-manual"}`:                     "server_id is required",
		} {
			r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/restore-backup", Body: body}).expect(t, http.StatusBadRequest)
			if !strings.Contains(string(r.Body), want) {
				t.Errorf("%s: body = %s, want %q", body, r.Body, want)
			}
		}
		if e.agent.count() != n {
			t.Fatal("a refused restore reached the agent")
		}
	})
	t.Run("list-orphans", func(t *testing.T) {
		var res command.ListOrphansResponse
		owner.succeed(t, get("/api/v1/storage/list-orphans", url.Values{"server_id": {sid}, "name": {"disk"}}), &res)
		if res.Name != "disk" || len(res.Orphans) != 1 || res.Orphans[0].AppName != "Shop" || res.Orphans[0].Copies != 3 {
			t.Fatalf("orphans = %+v", res)
		}
		owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/storage/list-orphans", Invalid: true,
			Query: url.Values{"server_id": {sid}, "name": {"Bad Name"}}}).expect(t, http.StatusBadRequest)
	})
	t.Run("delete-orphan", func(t *testing.T) {
		var res command.DeleteOrphanResponse
		owner.succeed(t, post("/api/v1/storage/delete-orphan", map[string]any{"server_id": sid, "name": "disk", "app_id": "gone-app"}), &res)
		if res.AppID != "gone-app" || res.Deleted != 6 {
			t.Fatalf("deleted = %+v", res)
		}
		e.agent.fail(command.TypeStorageDeleteOrphan, "the app Shop is on this server: its backups are not orphans")
		owner.failWith(t, post("/api/v1/storage/delete-orphan", map[string]any{"server_id": sid, "name": "disk", "app_id": anApp}),
			"the app Shop is on this server: its backups are not orphans")
		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/storage/delete-orphan", Invalid: true,
			Body: map[string]any{"server_id": sid, "name": "disk", "app_id": "../x"}}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "invalid app id") {
			t.Fatalf("body = %s", r.Body)
		}
	})
	t.Run("delete-app carries delete_backups", func(t *testing.T) {
		owner.succeed(t, post("/api/v1/app/delete-app", map[string]any{"server_id": sid, "app_id": anApp, "delete_backups": true}), nil)
		got := e.agent.seen(command.TypeAppDelete)
		if in := decode[command.DeleteAppRequest](got[len(got)-1].Payload); !in.DeleteBackups || in.DeleteVolumes {
			t.Fatalf("agent got %+v", in)
		}
	})
}
