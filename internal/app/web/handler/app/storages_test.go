package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

const diskBody = `{"name":"disk","type":"local","local":{"path":"/mnt/usb"}}`

func TestStorageRoutesDispatch(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
		typ     command.Type
	}{
		{"list", func(h *Handler) http.HandlerFunc { return h.ListStorages },
			httptest.NewRequest("GET", "/api/v1/storage/list?server_id=s1", nil), command.TypeStorageList},
		{"test", func(h *Handler) http.HandlerFunc { return h.TestStorage },
			httptest.NewRequest("POST", "/api/v1/storage/test?server_id=s1", strings.NewReader(diskBody)), command.TypeStorageTest},
		{"save", func(h *Handler) http.HandlerFunc { return h.SaveStorage },
			httptest.NewRequest("POST", "/api/v1/storage/save?server_id=s1", strings.NewReader(diskBody)), command.TypeStorageSave},
		{"delete", func(h *Handler) http.HandlerFunc { return h.DeleteStorage },
			httptest.NewRequest("POST", "/api/v1/storage/delete?server_id=s1", strings.NewReader(`{"name":"disk"}`)), command.TypeStorageDelete},
	}
	for _, tc := range cases {
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

func TestStorageRoutesRejectBadInput(t *testing.T) {
	type route = func(*Handler) http.HandlerFunc
	list := route(func(h *Handler) http.HandlerFunc { return h.ListStorages })
	test := route(func(h *Handler) http.HandlerFunc { return h.TestStorage })
	save := route(func(h *Handler) http.HandlerFunc { return h.SaveStorage })
	del := route(func(h *Handler) http.HandlerFunc { return h.DeleteStorage })
	cases := []struct {
		name    string
		handler route
		target  string
		body    string
		want    int
	}{
		{"list without server", list, "/x", "", http.StatusBadRequest},
		{"list foreign server", list, "/x?server_id=other", "", http.StatusForbidden},
		{"test foreign server", test, "/x?server_id=other", diskBody, http.StatusForbidden},
		{"test bad json", test, "/x?server_id=s1", `{`, http.StatusBadRequest},
		{"test bad name", test, "/x?server_id=s1", `{"name":"Disk","type":"local","local":{"path":"/mnt"}}`, http.StatusBadRequest},
		{"save foreign server", save, "/x?server_id=other", diskBody, http.StatusForbidden},
		{"save bad json", save, "/x?server_id=s1", `{`, http.StatusBadRequest},
		{"save relative path", save, "/x?server_id=s1", `{"name":"disk","type":"local","local":{"path":"backups"}}`, http.StatusBadRequest},
		{"save public http", save, "/x?server_id=s1", `{"name":"s","type":"s3","s3":{"endpoint":"http://s3.example.com","bucket":"bkt","access_key_id":"A","secret_access_key":"C"}}`, http.StatusBadRequest},
		{"delete foreign server", del, "/x?server_id=other", `{"name":"disk"}`, http.StatusForbidden},
		{"delete bad name", del, "/x?server_id=s1", `{"name":"../x"}`, http.StatusBadRequest},
		{"delete bad json", del, "/x?server_id=s1", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			r := httptest.NewRequest("POST", tc.target, strings.NewReader(tc.body))
			if w := do(tc.handler(h), authed(r)); w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if fd.last.Type != "" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
	h, _, _ := newHandler(t)
	if w := do(h.SaveStorage, httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(diskBody))); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}

// failingDispatcher refuses every dispatch (bus down).
type failingDispatcher struct{}

func (failingDispatcher) Dispatch(context.Context, port.DispatchInput) (string, error) {
	return "", errors.New("bus down")
}

func TestStorageRoutesReportDispatchFailures(t *testing.T) {
	h := NewHandler(&Deps{
		Logger:            logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher: failingDispatcher{},
		AppRepository:     &fakeAppRepo{},
		Servers:           &fakeServerAccess{owned: map[string]bool{"s1": true}},
	})
	for want, call := range map[string]func() *httptest.ResponseRecorder{
		"failed to list storages": func() *httptest.ResponseRecorder {
			return do(h.ListStorages, authed(httptest.NewRequest("GET", "/x?server_id=s1", nil)))
		},
		"failed to test storage": func() *httptest.ResponseRecorder {
			return do(h.TestStorage, authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(diskBody))))
		},
		"failed to save storage": func() *httptest.ResponseRecorder {
			return do(h.SaveStorage, authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(diskBody))))
		},
		"failed to remove storage": func() *httptest.ResponseRecorder {
			return do(h.DeleteStorage, authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{"name":"disk"}`))))
		},
	} {
		if w := call(); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", want, w.Code, w.Body.String())
		}
	}
}

// fakeServerStorages answers ListForServer from rows (or err).
type fakeServerStorages struct {
	rows []model.StorageSummary
	err  error
}

func (f fakeServerStorages) ListForServer(context.Context, string) ([]model.StorageSummary, error) {
	return f.rows, f.err
}
func (fakeServerStorages) Upsert(context.Context, string, model.StorageSummary) error { return nil }
func (fakeServerStorages) Delete(context.Context, string, string) error               { return nil }
func (fakeServerStorages) ReplaceForServer(context.Context, string, []model.StorageSummary) error {
	return nil
}

func storagesHandler(ss port.ServerStorageRepository) *Handler {
	return NewHandler(&Deps{
		Logger:                  logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher:       &fakeDispatcher{},
		AppRepository:           &fakeAppRepo{},
		ServerStorageRepository: ss,
		Servers:                 &fakeServerAccess{owned: map[string]bool{"s1": true}},
	})
}

func TestGetStoragesReadsTheIndex(t *testing.T) {
	h := storagesHandler(fakeServerStorages{rows: []model.StorageSummary{{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"}}})
	w := do(h.GetStorages, authed(httptest.NewRequest("GET", "/api/v1/storage/get-storages?server_id=s1", nil)))
	var body struct {
		Data GetStoragesResponse `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil ||
		len(body.Data.Storages) != 1 || body.Data.Storages[0].Location != "/mnt/usb" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	for target, want := range map[string]int{"/x": http.StatusBadRequest, "/x?server_id=bad": http.StatusForbidden} {
		if w := do(h.GetStorages, authed(httptest.NewRequest("GET", target, nil))); w.Code != want {
			t.Errorf("%s = %d, want %d", target, w.Code, want)
		}
	}
	failing := storagesHandler(fakeServerStorages{err: errors.New("db down")})
	if w := do(failing.GetStorages, authed(httptest.NewRequest("GET", "/x?server_id=s1", nil))); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "failed to load storages") {
		t.Fatalf("index error = %d %s", w.Code, w.Body.String())
	}
}
