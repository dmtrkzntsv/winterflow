package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/monitoring"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/transport/bus"
)

// TestMetricsRoutes: a server.metrics event on the events queue becomes a
// point on the server and app charts and the dashboard overview.
func TestMetricsRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)

	cpu, appCPU := 37.5, 4.0
	body, err := json.Marshal(metrics.Sample{
		TS: time.Now().Unix(),
		Server: metrics.ServerSample{CPU: &cpu, MemUsed: 2e9, MemTotal: 8e9,
			Disks: []metrics.Disk{{Mount: "/", Used: 40, Total: 100}}},
		Apps: []metrics.AppSample{{AppID: anApp, CPU: &appCPU}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventServerMetrics, Payload: body})

	q := url.Values{"server_id": {sid}, "range": {"1h"}}
	var series monitoring.ServerSeriesResponse
	eventually(t, func() bool {
		owner.get(t, "/api/v1/metrics/server", q).expect(t, http.StatusOK).data(t, &series)
		return len(series.Points) == 1
	})
	if p := series.Points[0]; p["cpu"] != 37.5 || p["cpu_max"] != 37.5 || p["disk_pct"] != 40 || series.IntervalSeconds != 60 {
		t.Fatalf("server series = %+v", series)
	}
	// The app is unknown to the API (never saved), so it has no rows.
	if len(series.Apps) != 0 {
		t.Fatalf("apps = %+v", series.Apps)
	}

	var app monitoring.SeriesResponse
	owner.get(t, "/api/v1/metrics/app", url.Values{"server_id": {sid}, "app_id": {anApp}, "range": {"30d"}}).
		expect(t, http.StatusOK).data(t, &app)
	if app.IntervalSeconds != 28800 || len(app.Points) != 0 {
		t.Fatalf("app series = %+v", app)
	}

	var ov monitoring.OverviewResponse
	owner.get(t, "/api/v1/metrics/overview", nil).expect(t, http.StatusOK).data(t, &ov)
	if len(ov.Servers) != 1 || ov.Servers[0].ServerID != sid || ov.Servers[0].Latest == nil ||
		*ov.Servers[0].Latest.Server.CPU != 37.5 || len(ov.Servers[0].Sparkline) != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	// An unknown range is outside the documented enum: 400, not a guess.
	owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/metrics/server", Invalid: true,
		Query: url.Values{"server_id": {sid}, "range": {"2h"}}}).expect(t, http.StatusBadRequest)
}
