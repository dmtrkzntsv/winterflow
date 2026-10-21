package bootstrap

import (
	"context"
	enrollmentrepo "winterflow/internal/infra/db/repository/enrollment"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/alerting"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/domain/usecase/agentauth"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db"
	alertrepo "winterflow/internal/infra/db/repository/alert"
	apprepo "winterflow/internal/infra/db/repository/app"
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
	serverstoragerepo "winterflow/internal/infra/db/repository/serverstorage"
	sharedvarrepo "winterflow/internal/infra/db/repository/sharedvar"
	templaterepo "winterflow/internal/infra/db/repository/template"
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
func WireCore(ctx context.Context, b bus.Bus, conn *db.DB, cfg *config.ServerConfig, log *logger.Logger, opts ...CoreOption) (*Deps, *serverrepo.Repo) {
	var o coreOptions
	for _, opt := range opts {
		opt(&o)
	}
	r, err := newRepos(conn, log)
	if err != nil {
		// Only an unknown driver name fails, which db.Open never returns.
		log.Fatalf("wire repositories: %v", err)
	}

	nm := notificationsvc.NewNotificationManager(log)
	router := hubs.SingleQueue(cfg)
	var picker *hubs.Picker
	if o.hubRegistry != nil {
		router = hubs.NewRouter(r.server, cfg)
		picker = hubs.NewPicker(o.hubRegistry, r.server, log)
	}
	dispatcher := dispatch.NewManager(b, router, nm, log)
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
	}

	// Alerts: the evaluator rides the events sink (samples, statuses,
	// liveness sweep); the notifier sends through the org's channels.
	al := wireAlerts(ctx, r.alert, history, newStatusFanout(r.server, nm, log), cfg, log)

	// After the alert warm start: the first rollup tick would otherwise
	// hold SQLite's single connection while Load reads.
	if cfg.IsMetricsEnabled() {
		startMetricsMaintenance(ctx, r.metric, latest, log)
	}

	startResponseSubscriber(ctx, b, dispatcher, cfg, log)
	vars := newVarsSync(ctx, dispatcher, sharedvars.Deps{
		Repo: r.sharedVar, Servers: r.server, Notifications: nm, Revisions: statusCache, Log: log,
	}, log)
	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, indexSyncs{
		newDomainSync(ctx, dispatcher, r.serverDomain, log),
		newStorageSync(ctx, dispatcher, r.serverStorage, log),
	}, vars, ms, latest, al.evaluator)

	// The rollout queue is built here (one value for the routes and the
	// worker) but its worker is NOT started: its first pass may claim a
	// rollout whose lease expired and dispatch at once, which is lost while
	// nothing consumes the request queue (standalone: the in-process bridge).
	// Each topology calls StartRollouts once commands can flow.
	instances := newInstances(r, dispatcher, statusCache, log)
	queue := newRollouts(r, instances, dispatcher, statusCache, nm, o.rolloutTiming, log)

	// Distributed only: hubs ask the API whether a connecting agent's
	// certificate is still its server's active one, and a deleted server's
	// agent is dropped by a broadcast. Standalone has no hubs.
	var revoker port.Revoker
	if !cfg.IsStandalone() {
		if err := agentauth.StartResponder(ctx, b, cfg, r.server, log); err != nil {
			log.Fatalf("failed to subscribe to %s: %v", cfg.GetHubRequestsChannel(), err)
		}
		revoker = agentauth.NewBusRevoker(b, cfg)
	}

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
		TemplateRepository:       r.template,
		CatalogRepository:        r.template,
		Instances:                instances,
		Rollouts:                 queue,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
		StatusCache:              statusCache,
		MetricRepository:         r.metric,
		LatestMetrics:            latest,
		AlertRepository:          r.alert,
		AlertNotifier:            al.notifier,
		ChannelBox:               al.box,
		HubPicker:                picker,
		EnrollmentRepository:     r.enrollment,
		Revoker:                  revoker,
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
	template      *templaterepo.Repo
	rollout       *rolloutrepo.Repo
	alert         *alertrepo.Repo
	enrollment    *enrollmentrepo.Repo
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
	if r.template, err = templaterepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.rollout, err = rolloutrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	if r.alert, err = alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID); err != nil {
		return r, err
	}
	r.enrollment, err = enrollmentrepo.New(conn.Tx, conn.Driver)
	return r, err
}
