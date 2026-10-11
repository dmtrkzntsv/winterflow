package monitoring

import (
	"net/http"
	"time"

	"winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/service/status"
)

// sparklineFields are the host fields of the dashboard's 1-hour sparklines.
var sparklineFields = []string{"cpu", "mem_used", "mem_total", "disk_pct"}

// AppCounts counts a server's apps by their live status.
type AppCounts struct {
	Running     int `json:"running"`
	Stopped     int `json:"stopped"`
	Problematic int `json:"problematic"`
}

// ServerOverview is one dashboard health widget.
type ServerOverview struct {
	ServerID string          `json:"server_id"`
	Liveness status.Liveness `json:"liveness"`
	// Latest is the newest host sample; null before the first one arrives
	// (or after an hour without samples).
	Latest    *metrics.Latest `json:"latest"`
	Sparkline []metrics.Point `json:"sparkline"`
	Apps      AppCounts       `json:"apps"`
}

// OverviewResponse is the data of GET /api/v1/metrics/overview.
type OverviewResponse struct {
	Servers []ServerOverview `json:"servers"`
}

// countApps buckets live app statuses: active/restarting/idle run,
// stopped is stopped, problematic is problematic, unknown is not counted.
func countApps(apps []command.AppStatus) AppCounts {
	var c AppCounts
	for _, a := range apps {
		switch a.StatusCode {
		case command.ContainerStatusActive, command.ContainerStatusRestarting, command.ContainerStatusIdle:
			c.Running++
		case command.ContainerStatusStopped:
			c.Stopped++
		case command.ContainerStatusProblematic:
			c.Problematic++
		}
	}
	return c
}

// Overview serves GET /api/v1/metrics/overview: every server of the
// caller's organizations, one tier-1 query each.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	userID, ok := util.RequireUser(w, r)
	if !ok {
		return
	}
	if h.disabled {
		util.Success(w, "ok", OverviewResponse{Servers: []ServerOverview{}})
		return
	}
	servers, err := h.servers.GetServers(r.Context(), userID)
	if err != nil {
		util.Error(w, "failed to load servers", nil)
		return
	}
	now := h.now()
	out := make([]ServerOverview, 0, len(servers))
	for _, s := range servers {
		o := ServerOverview{
			ServerID:  s.ID,
			Liveness:  h.status.ServerLiveness(s.ID, now),
			Sparkline: []metrics.Point{},
			Apps:      countApps(h.status.AppStatuses(s.ID, now)),
		}
		if l, ok := h.latest.Get(s.ID); ok {
			o.Latest = &l
		}
		rows, err := h.repo.Series(r.Context(), s.ID, "", metrics.Tier1, now.Add(-time.Hour))
		if err != nil {
			h.log.Error("metrics: overview sparkline", "error", err, "server_id", s.ID)
		}
		for _, p := range toPoints(rows) {
			o.Sparkline = append(o.Sparkline, p.Pick(sparklineFields...))
		}
		out = append(out, o)
	}
	util.Success(w, "ok", OverviewResponse{Servers: out})
}
