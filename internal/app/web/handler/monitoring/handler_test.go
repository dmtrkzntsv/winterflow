package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"
	webutil "winterflow/internal/app/web/util"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/logger"
)

// fakeServers owns srv-1 for u1; every other port method panics if called.
type fakeServers struct {
	port.ServerRepository
	err error
}

func (f fakeServers) UserOwnsServer(_ context.Context, userID, serverID string) (bool, error) {
	return userID == "u1" && serverID == "srv-1", f.err
}

func (f fakeServers) GetServers(_ context.Context, userID string) ([]model.Server, error) {
	if f.err != nil {
		return nil, f.err
	}
	if userID != "u1" {
		return nil, nil
	}
	return []model.Server{{ID: "srv-1"}}, nil
}

type seriesCall struct {
	serverID, appID string
	tier            metrics.Tier
	since           time.Time
}

type fakeRepo struct {
	port.MetricRepository
	calls []seriesCall
	rows  []metrics.Bucket
	apps  map[string][]metrics.Bucket
	err   error
}

func (f *fakeRepo) Series(_ context.Context, serverID, appID string, tier metrics.Tier, since time.Time) ([]metrics.Bucket, error) {
	f.calls = append(f.calls, seriesCall{serverID, appID, tier, since})
	return f.rows, f.err
}

func (f *fakeRepo) AppsSeries(context.Context, string, metrics.Tier, time.Time) (map[string][]metrics.Bucket, error) {
	return f.apps, f.err
}

var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func newHandler(repo *fakeRepo, servers fakeServers) (*Handler, *status.Cache, *metrics.LatestStore) {
	cache := status.NewCache(time.Minute)
	latest := metrics.NewLatestStore()
	h := NewHandler(&Deps{
		Logger:      logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		Servers:     servers,
		Metrics:     repo,
		Latest:      latest,
		StatusCache: cache,
	})
	h.now = func() time.Time { return now }
	return h, cache, latest
}

func get(h http.HandlerFunc, url, userID string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, url, nil)
	if userID != "" {
		r = webutil.WithPrincipal(r, model.Principal{UserID: userID, Kind: model.TokenSession})
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var body struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	return body.Data
}

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := map[string]bool{ // "METHOD path" -> documents 403
		"GET /api/v1/metrics/server":   true,
		"GET /api/v1/metrics/app":      true,
		"GET /api/v1/metrics/overview": false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || len(rt.Middleware) != 0 {
			t.Errorf("%s: want a synchronous User route without middleware", key)
		}
		got[key] = slices.Contains(rt.Errors, http.StatusForbidden)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestServerSeriesMapsRangeToTier(t *testing.T) {
	cases := map[string]struct {
		tier     metrics.Tier
		span     time.Duration
		interval int
	}{
		"1h":  {metrics.Tier1, time.Hour, 60},
		"12h": {metrics.Tier10, 12 * time.Hour, 600},
		"24h": {metrics.Tier20, 24 * time.Hour, 1200},
		"7d":  {metrics.Tier120, 7 * 24 * time.Hour, 7200},
		"30d": {metrics.Tier480, 30 * 24 * time.Hour, 28800},
	}
	for rng, want := range cases {
		repo := &fakeRepo{rows: []metrics.Bucket{{Start: now.Add(-time.Hour), Samples: 1, Data: metrics.Values{"cpu": 5}}},
			apps: map[string][]metrics.Bucket{
				"b": {{Start: now, Samples: 1, Data: metrics.Values{"cpu": 2, "mem_used": 3, "disk": 9}}},
				"a": {{Start: now, Samples: 1, Data: metrics.Values{"cpu": 1}}},
			}}
		h, _, _ := newHandler(repo, fakeServers{})
		w := get(h.ServerSeries, "/api/v1/metrics/server?server_id=srv-1&range="+rng, "u1")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", rng, w.Code, w.Body.String())
		}
		if c := repo.calls[0]; c.tier != want.tier || !c.since.Equal(now.Add(-want.span)) || c.appID != "" {
			t.Errorf("%s: series call = %+v", rng, c)
		}
		body := decode[ServerSeriesResponse](t, w)
		if body.IntervalSeconds != want.interval || len(body.Points) != 1 || body.Points[0]["cpu_max"] != 5 {
			t.Errorf("%s: body = %+v", rng, body)
		}
		// Apps sorted by id, trimmed to the stacked fields.
		if len(body.Apps) != 2 || body.Apps[0].AppID != "a" || len(body.Apps[1].Points[0]) != 5 {
			t.Errorf("%s: apps = %+v", rng, body.Apps)
		}
	}
}

func TestSeriesRejectsBadInput(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{}, fakeServers{})
	cases := []struct {
		name string
		fn   http.HandlerFunc
		url  string
		user string
		code int
	}{
		{"anonymous", h.ServerSeries, "/x?server_id=srv-1&range=1h", "", http.StatusUnauthorized},
		{"no server", h.ServerSeries, "/x?range=1h", "u1", http.StatusBadRequest},
		{"foreign server", h.ServerSeries, "/x?server_id=srv-2&range=1h", "u1", http.StatusForbidden},
		{"other user", h.AppSeries, "/x?server_id=srv-1&app_id=a&range=1h", "u2", http.StatusForbidden},
		{"bad range", h.ServerSeries, "/x?server_id=srv-1&range=2h", "u1", http.StatusBadRequest},
		{"no range", h.AppSeries, "/x?server_id=srv-1&app_id=a", "u1", http.StatusBadRequest},
		{"no app", h.AppSeries, "/x?server_id=srv-1&range=1h", "u1", http.StatusBadRequest},
	}
	for _, tc := range cases {
		if w := get(tc.fn, tc.url, tc.user); w.Code != tc.code {
			t.Errorf("%s: %d, want %d (%s)", tc.name, w.Code, tc.code, w.Body.String())
		}
	}
	// The ownership check fails closed on a lookup error.
	h2, _, _ := newHandler(&fakeRepo{}, fakeServers{err: errors.New("db down")})
	if w := get(h2.ServerSeries, "/x?server_id=srv-1&range=1h", "u1"); w.Code != http.StatusForbidden {
		t.Errorf("lookup error: %d", w.Code)
	}
}

func TestAppSeries(t *testing.T) {
	repo := &fakeRepo{rows: []metrics.Bucket{}}
	h, _, _ := newHandler(repo, fakeServers{})
	w := get(h.AppSeries, "/x?server_id=srv-1&app_id=app-9&range=7d", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if c := repo.calls[0]; c.serverID != "srv-1" || c.appID != "app-9" || c.tier != metrics.Tier120 {
		t.Fatalf("call = %+v", c)
	}
	body := decode[SeriesResponse](t, w)
	if body.Points == nil || len(body.Points) != 0 || body.IntervalSeconds != 7200 {
		t.Fatalf("empty series = %+v (points must be [], not null)", body)
	}
}

func TestSeriesRepositoryError(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{err: errors.New("boom")}, fakeServers{})
	if w := get(h.ServerSeries, "/x?server_id=srv-1&range=1h", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("server: %d", w.Code)
	}
	if w := get(h.AppSeries, "/x?server_id=srv-1&app_id=a&range=1h", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("app: %d", w.Code)
	}
}

func TestOverview(t *testing.T) {
	repo := &fakeRepo{rows: []metrics.Bucket{{Start: now.Add(-time.Minute), Samples: 1,
		Data: metrics.Values{"cpu": 10, "mem_used": 1, "mem_total": 2, "disk_pct": 50, "load1": 3}}}}
	h, cache, latest := newHandler(repo, fakeServers{})
	cache.SetAppStatus("srv-1", []command.AppStatus{
		{AppID: "a", StatusCode: command.ContainerStatusActive},
		{AppID: "b", StatusCode: command.ContainerStatusRestarting},
		{AppID: "c", StatusCode: command.ContainerStatusStopped},
		{AppID: "d", StatusCode: command.ContainerStatusProblematic},
		{AppID: "e", StatusCode: command.ContainerStatusUnknown},
	}, nil, now)
	cpu := 10.0
	latest.Set("srv-1", metrics.Latest{At: now, Server: metrics.ServerSample{CPU: &cpu}})

	w := get(h.Overview, "/api/v1/metrics/overview", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	body := decode[OverviewResponse](t, w)
	if len(body.Servers) != 1 {
		t.Fatalf("servers = %+v", body.Servers)
	}
	s := body.Servers[0]
	if s.Liveness != status.LivenessOnline || s.Apps != (AppCounts{Running: 2, Stopped: 1, Problematic: 1}) {
		t.Fatalf("overview = %+v", s)
	}
	if s.Latest == nil || *s.Latest.Server.CPU != 10 {
		t.Fatalf("latest = %+v", s.Latest)
	}
	if len(s.Sparkline) != 1 || s.Sparkline[0]["disk_pct"] != 50 {
		t.Fatalf("sparkline = %+v", s.Sparkline)
	}
	if _, ok := s.Sparkline[0]["load1"]; ok {
		t.Fatal("sparkline carries fields it does not chart")
	}
	if c := repo.calls[0]; c.tier != metrics.Tier1 || !c.since.Equal(now.Add(-time.Hour)) {
		t.Fatalf("sparkline query = %+v", c)
	}

	// No sample yet: latest is null, sparkline [].
	h2, _, _ := newHandler(&fakeRepo{err: errors.New("boom")}, fakeServers{})
	s = decode[OverviewResponse](t, get(h2.Overview, "/x", "u1")).Servers[0]
	if s.Latest != nil || s.Sparkline == nil || len(s.Sparkline) != 0 {
		t.Fatalf("empty overview = %+v", s)
	}
	// Another org's user sees nothing; a lookup error is a 400.
	if got := decode[OverviewResponse](t, get(h.Overview, "/x", "u2")); len(got.Servers) != 0 {
		t.Fatalf("u2 sees %+v", got)
	}
	h3, _, _ := newHandler(&fakeRepo{}, fakeServers{err: errors.New("db down")})
	if w := get(h3.Overview, "/x", "u1"); w.Code != http.StatusBadRequest {
		t.Fatalf("lookup error: %d", w.Code)
	}
	if w := get(h.Overview, "/x", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

// With METRICS_ENABLED=false the API stores nothing: the routes still answer
// 200 (after the auth and ownership checks) with empty data, and never touch
// the repository.
func TestDisabledServesEmpty(t *testing.T) {
	repo := &fakeRepo{err: errors.New("must not be called")}
	h, cache, latest := newHandler(repo, fakeServers{})
	h.disabled = true

	w := get(h.ServerSeries, "/x?server_id=srv-1&range=24h", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("server: %d %s", w.Code, w.Body.String())
	}
	sv := decode[ServerSeriesResponse](t, w)
	if sv.IntervalSeconds != 1200 || sv.Points == nil || len(sv.Points) != 0 || sv.Apps == nil || len(sv.Apps) != 0 {
		t.Fatalf("server = %+v", sv)
	}
	w = get(h.AppSeries, "/x?server_id=srv-1&app_id=a&range=7d", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("app: %d", w.Code)
	}
	if ap := decode[SeriesResponse](t, w); ap.Points == nil || len(ap.Points) != 0 || ap.IntervalSeconds != 7200 {
		t.Fatalf("app = %+v", ap)
	}
	cache.SetAppStatus("srv-1", []command.AppStatus{
		{AppID: "a", StatusCode: command.ContainerStatusActive},
		{AppID: "c", StatusCode: command.ContainerStatusStopped},
		{AppID: "d", StatusCode: command.ContainerStatusProblematic},
	}, nil, now)
	cpu := 10.0
	latest.Set("srv-1", metrics.Latest{At: now, Server: metrics.ServerSample{CPU: &cpu}})
	w = get(h.Overview, "/x", "u1")
	if w.Code != http.StatusOK {
		t.Fatalf("overview: %d", w.Code)
	}
	ov := decode[OverviewResponse](t, w)
	if len(ov.Servers) != 1 {
		t.Fatalf("overview = %+v", ov)
	}
	if s := ov.Servers[0]; s.ServerID != "srv-1" || s.Liveness != status.LivenessOnline ||
		s.Apps != (AppCounts{Running: 1, Stopped: 1, Problematic: 1}) ||
		s.Latest != nil || s.Sparkline == nil || len(s.Sparkline) != 0 {
		t.Fatalf("disabled overview = %+v", s)
	}
	if len(repo.calls) != 0 {
		t.Fatalf("repository called: %+v", repo.calls)
	}
	// Ownership is still enforced.
	if w := get(h.ServerSeries, "/x?server_id=srv-2&range=1h", "u1"); w.Code != http.StatusForbidden {
		t.Fatalf("foreign: %d", w.Code)
	}
}

// fakeCounter serves CountFiring.
type fakeCounter struct {
	counts map[string]int
	err    error
}

func (f fakeCounter) CountFiring(context.Context) (map[string]int, error) { return f.counts, f.err }

// The overview carries each server's firing-alert count; a count failure
// is logged and shows zero.
func TestOverviewCountsFiringAlerts(t *testing.T) {
	h, _, _ := newHandler(&fakeRepo{}, fakeServers{})
	h.alerts = fakeCounter{counts: map[string]int{"srv-1": 2, "srv-other": 5}}
	if s := decode[OverviewResponse](t, get(h.Overview, "/x", "u1")).Servers[0]; s.AlertsFiring != 2 {
		t.Fatalf("alerts_firing = %d", s.AlertsFiring)
	}
	h.alerts = fakeCounter{err: errors.New("db down")}
	w := get(h.Overview, "/x", "u1")
	if w.Code != http.StatusOK || decode[OverviewResponse](t, w).Servers[0].AlertsFiring != 0 {
		t.Fatalf("count error: %d %s", w.Code, w.Body.String())
	}
}
