package bootstrap

import (
	"context"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/service/alerting"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db"
	alertrepo "winterflow/internal/infra/db/repository/alert"
	apprepo "winterflow/internal/infra/db/repository/app"
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
	serverstoragerepo "winterflow/internal/infra/db/repository/serverstorage"
	sharedvarrepo "winterflow/internal/infra/db/repository/sharedvar"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/dispatch"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// WireCore builds the wiring both topologies share: DB-backed repositories and
// services, the notification manager, the command dispatcher, the status
// cache, and the response/events subscribers. The topologies differ only in
// the bus they pass (Redis vs in-process) and the extras they add afterwards
// (standalone: certs, in-process bridge, self-registration; api: nothing).
// The concrete server repository is returned alongside Deps for standalone's
// registration flow, which needs methods beyond the port.
func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerConfig, log *logger.Logger) (*Deps, *serverrepo.Repo) {
	r, err := newRepos(conn, log)
	if err != nil {
		// Only an unknown driver name fails, which db.Open never returns.
		log.Fatalf("wire repositories: %v", err)
	}

	nm := notificationsvc.NewNotificationManager(log)
	dispatcher := dispatch.NewManager(b, nm, cfg, log)
	statusCache := status.NewCache(statusTTL)

	// Metrics: the events sink stores server.metrics samples and the
	// maintenance loop rolls them up and prunes them. Both are off when
	// METRICS_ENABLED=false; the chart routes then serve empty series.
	latest := metrics.NewLatestStore()
	var ms metricsStore
	var history alerting.History
	if cfg.IsMetricsEnabled() {
		ms = r.metric
		history = r.metric
		go runMetricsMaintenance(ctx, r.metric, latest, metricsMaintenanceInterval, log)
	}

	// Alerts: the evaluator rides the events sink (samples, statuses,
	// liveness sweep); the notifier sends through the org's channels.
	al := wireAlerts(ctx, r.alert, history, newStatusFanout(r.server, nm, log), cfg, log)

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
	vars := newVarsSync(ctx, dispatcher, sharedvars.Deps{
		Repo: r.sharedVar, Servers: r.server, Notifications: nm, Revisions: statusCache, Log: log,
	}, log)
	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, indexSyncs{
		newDomainSync(ctx, dispatcher, r.serverDomain, log),
		newStorageSync(ctx, dispatcher, r.serverStorage, log),
	}, vars, ms, latest, al.evaluator)

	return &Deps{
		Log:                      log,
		Cfg:                      cfg,
		UserService:              r.user,
		ServerRepository:         r.server,
		AppRepository:            r.app,
		AppDomainRepository:      r.appDomain,
		ServerDomainRepository:   r.serverDomain,
		ServerStorageRepository:  r.serverStorage,
		SharedVariableRepository: r.sharedVar,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
		StatusCache:              statusCache,
		MetricRepository:         r.metric,
		LatestMetrics:            latest,
		AlertRepository:          r.alert,
		AlertNotifier:            al.notifier,
		ChannelBox:               al.box,
	}, r.server
}

// repos are the DB repositories, built for the connection's driver.
type repos struct {
	user          *userrepo.Repo
	server        *serverrepo.Repo
	app           *apprepo.Repo
	appDomain     *appdomainrepo.Repo
	serverDomain  *serverdomainrepo.Repo
	serverStorage *serverstoragerepo.Repo
	metric        *metricrepo.Repo
	sharedVar     *sharedvarrepo.Repo
	alert         *alertrepo.Repo
}

func newRepos(conn *db.DB, log *logger.Logger) (repos, error) {
	var (
		r   repos
		err error
	)
	if r.user, err = userrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.server, err = serverrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.app, err = apprepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.appDomain, err = appdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.serverStorage, err = serverstoragerepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.metric, err = metricrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.sharedVar, err = sharedvarrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	r.alert, err = alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID)
	return r, err
}
