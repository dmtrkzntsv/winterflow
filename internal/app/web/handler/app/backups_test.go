package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func TestBackupRoutesDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
		typ     command.Type
	}{
		{"update", func(h *Handler) http.HandlerFunc { return h.UpdateBackups },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backups":[{"name":"db","every":"1h"}]}`)), command.TypeBackupUpdate},
		{"points", func(h *Handler) http.HandlerFunc { return h.ListRestorePoints },
			httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil), command.TypeBackupPoints},
		{"run", func(h *Handler) http.HandlerFunc { return h.RunBackup },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)), command.TypeBackupRun},
		{"delete point", func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupDeletePoint},
		{"restore", func(h *Handler) http.HandlerFunc { return h.RestoreBackup },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupRestore},
		{"orphans", func(h *Handler) http.HandlerFunc { return h.ListOrphans },
			httptest.NewRequest("GET", "/x?server_id=s1&name=disk", nil), command.TypeStorageOrphans},
		{"delete orphan", func(h *Handler) http.HandlerFunc { return h.DeleteOrphan },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","name":"disk","app_id":"gone"}`)), command.TypeStorageDeleteOrphan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			w := do(tc.handler(h), authed(tc.request))
			if w.Code != http.StatusAccepted || decodeRequestID(t, w) != "req-123" {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if fd.last.Type != tc.typ || fd.last.AgentID != "s1" || fd.last.UserID != "u1" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
}

func TestBackupRoutesRejectBadInput(t *testing.T) {
	type route = func(*Handler) http.HandlerFunc
	update := route(func(h *Handler) http.HandlerFunc { return h.UpdateBackups })
	points := route(func(h *Handler) http.HandlerFunc { return h.ListRestorePoints })
	run := route(func(h *Handler) http.HandlerFunc { return h.RunBackup })
	del := route(func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint })
	restore := route(func(h *Handler) http.HandlerFunc { return h.RestoreBackup })
	orphans := route(func(h *Handler) http.HandlerFunc { return h.ListOrphans })
	delOrphan := route(func(h *Handler) http.HandlerFunc { return h.DeleteOrphan })
	for _, tc := range []struct {
		name    string
		handler route
		method  string
		target  string
		body    string
		want    int
		msg     string
	}{
		{"update bad json", update, "POST", "/x", `{`, http.StatusBadRequest, "invalid request body"},
		{"update no server", update, "POST", "/x", `{"app_id":"a1"}`, http.StatusBadRequest, "server_id is required"},
		{"update bad app", update, "POST", "/x", `{"server_id":"s1","app_id":".."}`, http.StatusBadRequest, "invalid app id"},
		{"update enabled without storage", update, "POST", "/x", `{"server_id":"s1","app_id":"a1","backups":[{"name":"db","enabled":true}]}`,
			http.StatusBadRequest, "an enabled backup needs a storage"},
		{"update no backups list", update, "POST", "/x", `{"server_id":"s1","app_id":"a1"}`, http.StatusBadRequest, "backups is required"},
		{"update null backups list", update, "POST", "/x", `{"server_id":"s1","app_id":"a1","backups":null}`, http.StatusBadRequest, "backups is required"},
		{"update foreign server", update, "POST", "/x", `{"server_id":"other","app_id":"a1","backups":[]}`, http.StatusForbidden, ""},
		{"run bad backup", run, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"Bad"}`, http.StatusBadRequest, "is not a valid backup name"},
		{"run foreign server", run, "POST", "/x", `{"server_id":"other","app_id":"a1","backup":"db"}`, http.StatusForbidden, ""},
		{"delete bad point", del, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"db","point":"x"}`, http.StatusBadRequest, "invalid restore point id"},
		{"points no server", points, "GET", "/x?app_id=a1&backup=db", "", http.StatusBadRequest, "server_id is required"},
		{"points foreign server", points, "GET", "/x?server_id=other&app_id=a1&backup=db", "", http.StatusForbidden, ""},
		{"points no backup", points, "GET", "/x?server_id=s1&app_id=a1", "", http.StatusBadRequest, "is not a valid backup name"},
		{"restore bad point", restore, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"db","point":"latest"}`, http.StatusBadRequest, "invalid restore point id"},
		{"restore foreign server", restore, "POST", "/x", `{"server_id":"other","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`, http.StatusForbidden, ""},
		{"orphans bad name", orphans, "GET", "/x?server_id=s1&name=Disk", "", http.StatusBadRequest, "is not a valid storage name"},
		{"orphans foreign server", orphans, "GET", "/x?server_id=other&name=disk", "", http.StatusForbidden, ""},
		{"delete orphan bad app", delOrphan, "POST", "/x", `{"server_id":"s1","name":"disk","app_id":"a/b"}`, http.StatusBadRequest, "invalid app id"},
		{"delete orphan foreign server", delOrphan, "POST", "/x", `{"server_id":"other","name":"disk","app_id":"gone"}`, http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			w := do(tc.handler(h), authed(r))
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.msg) {
				t.Fatalf("status %d, want %d %q: %s", w.Code, tc.want, tc.msg, w.Body.String())
			}
			if fd.last.Type != "" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
	h, _, _ := newHandler(t)
	if w := do(h.RunBackup, httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`))); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}

func TestBackupRoutesReportFailures(t *testing.T) {
	failing := NewHandler(&Deps{
		Logger:            logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher: failingDispatcher{},
		AppRepository:     &fakeAppRepo{},
		Servers:           &fakeServerAccess{owned: map[string]bool{"s1": true}},
	})
	for want, w := range map[string]*httptest.ResponseRecorder{
		"failed to run backup": do(failing.RunBackup, authed(httptest.NewRequest("POST", "/x",
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)))),
		"failed to list restore points":   do(failing.ListRestorePoints, authed(httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil))),
		"failed to list orphaned backups": do(failing.ListOrphans, authed(httptest.NewRequest("GET", "/x?server_id=s1&name=disk", nil))),
		"failed to restore backup": do(failing.RestoreBackup, authed(httptest.NewRequest("POST", "/x",
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)))),
	} {
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", want, w.Code, w.Body.String())
		}
	}
	// A rejection by the usecase (a storage the server doesn't have) is a
	// 400 with its reason.
	h := storagesHandler(fakeServerStorages{rows: []model.StorageSummary{{Name: "disk"}}})
	w := do(h.UpdateBackups, authed(httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"server_id":"s1","app_id":"a1","backups":[{"name":"db","enabled":true,"storage":"nas"}]}`))))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `backup db: there is no storage \"nas\" on this server`) {
		t.Fatalf("rejected = %d %s", w.Code, w.Body.String())
	}
}
