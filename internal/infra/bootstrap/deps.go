package bootstrap

import (
	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/alerting"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/authn"
	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/domain/usecase/rollouts"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/secretbox"
)

// Deps holds the fully-constructed dependencies a web server needs to run.
//
// Each binary (standalone, api, …) builds a Deps with the implementations that
// fit its topology — DB-backed services for standalone, Redis/Bus-backed
// services for the distributed API — and hands the same struct to web.NewServer.
// There is intentionally no factory abstraction: wiring happens once, in plain
// constructor calls, inside the Bootstrap* functions.
type Deps struct {
	Log *logger.Logger
	Cfg *config.ServerConfig

	UserService port.UserService
	// Authn issues, authenticates and revokes access tokens (sessions,
	// PATs, OAuth access tokens).
	Authn               *authn.UseCase
	ServerRepository    port.ServerRepository
	AppRepository       port.AppRepository
	AppDomainRepository port.AppDomainRepository
	// ServerDomainRepository indexes each server's domain registrations.
	ServerDomainRepository port.ServerDomainRepository
	// ServerStorageRepository indexes each server's storages (no secrets).
	ServerStorageRepository port.ServerStorageRepository
	// SharedVariableRepository stores org- and server-level shared variables.
	SharedVariableRepository port.SharedVariableRepository
	// TemplateRepository stores templates, their revisions and instances.
	TemplateRepository port.TemplateRepository
	// CatalogRepository is the same repository as the catalog sync sees
	// it (StartCatalog).
	CatalogRepository port.CatalogRepository
	// Instances is the templates usecase the instance routes and the
	// rollout queue's upgrade steps share.
	Instances *templates.UseCase
	// Rollouts is the rollout queue WireCore builds; its worker is started
	// by StartRollouts once commands can flow (after the in-process bridge
	// or the response subscriber). Every handler that queues a rollout must
	// use this one value (its Wake reaches that worker).
	Rollouts            *rollouts.UseCase
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
	// MetricRepository serves the monitoring charts; LatestMetrics holds
	// each server's newest host sample for the dashboard.
	MetricRepository port.MetricRepository
	LatestMetrics    *metrics.LatestStore
	// AlertRepository stores channels, rules, overrides, states and
	// history; AlertNotifier sends (the Test button uses it directly).
	AlertRepository port.AlertRepository
	AlertNotifier   *alerting.Notifier
	// ChannelBox encrypts channel URLs; nil when JWT_SECRET is unset
	// (saving or testing a channel then answers 400).
	ChannelBox *secretbox.Box
	// HubPicker assigns an enrolling server its hub from the live hub list;
	// nil in standalone (no hubs).
	HubPicker *hubs.Picker
	// EnrollmentRepository stores agent enrollments; Enrollment is the
	// usecase over it, wired by BootstrapAPI only (nil in standalone, where
	// the enroll routes are not mounted and codes come from registrations).
	EnrollmentRepository port.EnrollmentRepository
	Enrollment           *enrollment.UseCase
	// HubTokenRepository stores hub join tokens; HubJoin is the usecase
	// over it, wired by BootstrapAPI only (nil in standalone, where the hub
	// enroll route is not mounted).
	HubTokenRepository port.HubTokenRepository
	HubJoin            *hubjoin.UseCase
	// HubCertRepository records the hub certificates the API trusts;
	// agents pin the active set (enrollment status, agent hub answers).
	// Built in both topologies, used only where those usecases are wired.
	HubCertRepository port.HubCertRepository
	// AgentHub answers agents asking which hub to connect to; wired by
	// BootstrapAPI only (nil in standalone, where its route is not mounted).
	AgentHub *agenthub.UseCase
	// Revoker tells the hubs to drop a deleted server's agent. Nil until
	// the bus implementation is wired (distributed) and in standalone.
	Revoker port.Revoker
}
