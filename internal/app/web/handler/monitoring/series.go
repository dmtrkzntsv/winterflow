package monitoring

import (
	"net/http"
	"sort"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/metrics"
)

// SeriesResponse is one chart series: points oldest first, spaced
// interval_seconds apart where the target reported.
type SeriesResponse struct {
	IntervalSeconds int             `json:"interval_seconds"`
	Points          []metrics.Point `json:"points"`
}

// AppSeries is one app's CPU and memory points for the stacked charts.
type AppSeries struct {
	AppID  string          `json:"app_id"`
	Points []metrics.Point `json:"points"`
}

// ServerSeriesResponse is a server's series plus its apps' CPU/memory.
type ServerSeriesResponse struct {
	IntervalSeconds int             `json:"interval_seconds"`
	Points          []metrics.Point `json:"points"`
	Apps            []AppSeries     `json:"apps"`
}

// stackedFields are the app fields the server page stacks per app.
var stackedFields = []string{"cpu", "mem_used"}

// toPoints renders stored rows as chart points.
func toPoints(rows []metrics.Bucket) []metrics.Point {
	out := make([]metrics.Point, 0, len(rows))
	for _, b := range rows {
		out = append(out, metrics.ToPoint(b))
	}
	return out
}

// target validates the shared query: server_id present and owned by the
// caller, range known. It writes the error itself; ok reports success.
func (h *Handler) target(w http.ResponseWriter, r *http.Request) (serverID string, tier metrics.Tier, since time.Time, ok bool) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return "", 0, time.Time{}, false
	}
	q := r.URL.Query()
	serverID = q.Get("server_id")
	if serverID == "" {
		util.Error(w, "server_id is required", nil)
		return "", 0, time.Time{}, false
	}
	if !util.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", 0, time.Time{}, false
	}
	tier, span, known := metrics.ParseRange(q.Get("range"))
	if !known {
		util.Error(w, "range must be one of 1h, 12h, 24h, 7d, 30d", nil)
		return "", 0, time.Time{}, false
	}
	return serverID, tier, h.now().Add(-span), true
}

// ServerSeries serves GET /api/v1/metrics/server.
func (h *Handler) ServerSeries(w http.ResponseWriter, r *http.Request) {
	serverID, tier, since, ok := h.target(w, r)
	if !ok {
		return
	}
	resp := ServerSeriesResponse{
		IntervalSeconds: int(tier.Duration().Seconds()),
		Points:          []metrics.Point{},
		Apps:            []AppSeries{},
	}
	if h.disabled {
		util.Success(w, "ok", resp)
		return
	}
	rows, err := h.repo.Series(r.Context(), serverID, "", tier, since)
	if err != nil {
		h.log.Error("metrics: server series", "error", err, "server_id", serverID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	byApp, err := h.repo.AppsSeries(r.Context(), serverID, tier, since)
	if err != nil {
		h.log.Error("metrics: apps series", "error", err, "server_id", serverID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	ids := make([]string, 0, len(byApp))
	for id := range byApp {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		pts := toPoints(byApp[id])
		for i, p := range pts {
			pts[i] = p.Pick(stackedFields...)
		}
		resp.Apps = append(resp.Apps, AppSeries{AppID: id, Points: pts})
	}
	resp.Points = toPoints(rows)
	util.Success(w, "ok", resp)
}

// AppSeries serves GET /api/v1/metrics/app. Rows are keyed by server and
// app, so an app id from another server simply has no points.
func (h *Handler) AppSeries(w http.ResponseWriter, r *http.Request) {
	serverID, tier, since, ok := h.target(w, r)
	if !ok {
		return
	}
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		util.Error(w, "app_id is required", nil)
		return
	}
	resp := SeriesResponse{IntervalSeconds: int(tier.Duration().Seconds()), Points: []metrics.Point{}}
	if h.disabled {
		util.Success(w, "ok", resp)
		return
	}
	rows, err := h.repo.Series(r.Context(), serverID, appID, tier, since)
	if err != nil {
		h.log.Error("metrics: app series", "error", err, "server_id", serverID, "app_id", appID)
		util.Error(w, "failed to load metrics", nil)
		return
	}
	resp.Points = toPoints(rows)
	util.Success(w, "ok", resp)
}
