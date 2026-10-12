package monitoring

import (
	"net/http"

	"winterflow/internal/app/web/api"
)

// SeriesQuery addresses one server's chart.
type SeriesQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Target server id."`
	Range    string `query:"range" required:"true" enum:"1h,12h,24h,7d,30d" description:"Chart range: 1h (1-minute points), 12h (10 min), 24h (20 min), 7d (2 h), 30d (8 h)."`
}

// AppSeriesQuery addresses one app's chart.
type AppSeriesQuery struct {
	ServerID string `query:"server_id" required:"true" description:"Server the app runs on."`
	AppID    string `query:"app_id" required:"true" description:"Target app id."`
	Range    string `query:"range" required:"true" enum:"1h,12h,24h,7d,30d" description:"Chart range (see /api/v1/metrics/server)."`
}

// Routes is the monitoring slice of the route table. Server-addressed
// routes check org ownership in the handler (403); the overview lists only
// the caller's servers.
func (h *Handler) Routes(api.Guards) []api.Route {
	server := []int{http.StatusBadRequest, http.StatusForbidden}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/server", Handler: h.ServerSeries, Auth: api.User,
			Summary: "A server's resource charts", Tag: "metrics",
			Description: "Average and peak (`<field>_max`) per point; `interval_seconds` is the point spacing, so a " +
				"larger gap between two points means the server did not report. `apps` holds each app's CPU and memory " +
				"for the stacked charts.",
			Query: SeriesQuery{}, Response: ServerSeriesResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/app", Handler: h.AppSeries, Auth: api.User,
			Summary: "An app's resource charts", Tag: "metrics",
			Query: AppSeriesQuery{}, Response: SeriesResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/metrics/overview", Handler: h.Overview, Auth: api.User,
			Summary: "Dashboard health of the caller's servers", Tag: "metrics",
			Response: OverviewResponse{}, Errors: []int{http.StatusBadRequest},
		},
	}
}
