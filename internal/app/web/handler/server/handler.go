package server

import (
	"context"
	"time"

	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// Claimer binds an enrollment's pairing code to an org (the enrollment
// usecase); nil where agents do not enroll by code (standalone).
type Claimer interface {
	Claim(ctx context.Context, code, orgID string) (model.Server, error)
}

type Handler struct {
	claims Claimer
	// claimLimit throttles pairing-code guesses per user.
	claimLimit *ratelimit.Limiter
	now        func() time.Time
	users      port.UserService
	servers    port.ServerRepository
	status     *status.Cache
	latest     *metrics.LatestStore
	revoker    port.Revoker
	cfg        *config.ServerConfig
	log        *logger.Logger
}

type Deps struct {
	// Enrollment claims pairing codes of enrolled agents; nil in standalone.
	Enrollment       Claimer
	Logger           *logger.Logger
	ServerRepository port.ServerRepository
	UserService      port.UserService
	StatusCache      *status.Cache
	// LatestMetrics is forgotten for a deleted server; nil skips.
	LatestMetrics *metrics.LatestStore
	// Revoker tells the hubs to drop a deleted server's agent; nil in
	// standalone.
	Revoker port.Revoker
	Cfg     *config.ServerConfig
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		claims:     d.Enrollment,
		claimLimit: ratelimit.New(0.1, 5),
		now:        time.Now,
		users:      d.UserService,
		servers:    d.ServerRepository,
		status:     d.StatusCache,
		latest:     d.LatestMetrics,
		revoker:    d.Revoker,
		cfg:        d.Cfg,
		log:        d.Logger,
	}
}
