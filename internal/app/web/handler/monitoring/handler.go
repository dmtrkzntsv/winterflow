// Package monitoring serves the metrics charts: per-server and per-app time
// series from metric_samples and the dashboard overview (latest sample,
// 1-hour sparklines, app counts) for the caller's servers.
package monitoring

import (
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/logger"
)

// Handler serves /api/v1/metrics/*.
type Handler struct {
	servers  port.ServerRepository
	repo     port.MetricRepository
	latest   *metrics.LatestStore
	status   *status.Cache
	log      *logger.Logger
	disabled bool
	now      func() time.Time
}

// Deps are the handler's collaborators (all from bootstrap.Deps).
type Deps struct {
	Logger      *logger.Logger
	Servers     port.ServerRepository
	Metrics     port.MetricRepository
	Latest      *metrics.LatestStore
	StatusCache *status.Cache
	// Disabled mirrors METRICS_ENABLED=false: the API stores nothing then,
	// so the routes answer 200 with empty series and an empty overview.
	Disabled bool
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{
		servers:  d.Servers,
		repo:     d.Metrics,
		latest:   d.Latest,
		status:   d.StatusCache,
		log:      d.Logger,
		disabled: d.Disabled,
		now:      time.Now,
	}
}
