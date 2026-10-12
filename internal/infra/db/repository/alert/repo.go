// Package alert implements port.AlertRepository over migration 0012:
// notification channels, alert rules with per-target overrides, the state
// of each (rule, target) and the event history. State transitions are
// compare-and-set, so several API replicas that evaluate the same events
// record each transition (and its event) once.
package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	channelRow       = sqlitegen.NotificationChannel
	ruleRow          = sqlitegen.AlertRule
	overrideRow      = sqlitegen.AlertOverride
	eventRow         = sqlitegen.AlertEvent
	firingRow        = sqlitegen.ListFiringAlertStatesRow
	orgFiringRow     = sqlitegen.ListOrgFiringAlertStatesRow
	countRow         = sqlitegen.CountFiringAlertsByServerRow
	dirServerRow     = sqlitegen.ListAlertDirectoryServersRow
	dirAppRow        = sqlitegen.ListAlertDirectoryAppsRow
	cutoffRow        = sqlitegen.GetAlertEventTrimCutoffRow
	getChannelParams = sqlitegen.GetNotificationChannelParams
	insertChanParams = sqlitegen.InsertNotificationChannelParams
	updateChanParams = sqlitegen.UpdateNotificationChannelParams
	deleteChanParams = sqlitegen.DeleteNotificationChannelParams
	successParams    = sqlitegen.RecordChannelSuccessParams
	failureParams    = sqlitegen.RecordChannelFailureParams
	getRuleParams    = sqlitegen.GetAlertRuleParams
	insertRuleParams = sqlitegen.InsertAlertRuleParams
	updateRuleParams = sqlitegen.UpdateAlertRuleParams
	deleteRuleParams = sqlitegen.DeleteAlertRuleParams
	targetOvrParams  = sqlitegen.ListTargetAlertOverridesParams
	upsertOvrParams  = sqlitegen.UpsertAlertOverrideParams
	deleteOvrParams  = sqlitegen.DeleteAlertOverrideParams
	fireParams       = sqlitegen.FireAlertStateParams
	resolveParams    = sqlitegen.ResolveAlertStateParams
	deleteStateParam = sqlitegen.DeleteAlertStateParams
	insertEventParam = sqlitegen.InsertAlertEventParams
	deliveriesParams = sqlitegen.SetAlertEventDeliveriesParams
	listEventsParams = sqlitegen.ListAlertEventsParams
	cutoffParams     = sqlitegen.GetAlertEventTrimCutoffParams
	deleteUpToParams = sqlitegen.DeleteAlertEventsUpToParams
)

type querier interface {
	ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error)
	MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error)
	GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error)

	ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error)
	GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error)
	InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error
	UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error)
	DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error)
	RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error
	RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error

	ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error)
	GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error)
	InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error
	UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error)
	DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error)

	ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error)
	ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error)
	UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error
	DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error)

	FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error)
	ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error)
	DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error
	ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error)
	ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error)
	CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error)

	InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error
	SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error
	ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error)
	DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error)
	ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error)
	GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error)
	DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error)

	ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error)
	ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error)
}

// Repo is the alerts repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
	// newID mints channel, rule and event ids (util.GenerateID).
	newID func() string
}

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger, newID func() string) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log, newID: newID}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log, newID: newID}, nil
	}
	return nil, fmt.Errorf("alert repository: unknown database driver %q", driver)
}

// notFound maps sql.ErrNoRows to alert.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return alert.ErrNotFound
	}
	return err
}

// encodeIDs stores a channel list: nil stays NULL (defaults/inherit), an
// empty list is "[]".
func encodeIDs(ids []string) *string {
	if ids == nil {
		return nil
	}
	b, _ := json.Marshal(ids) // a []string always marshals
	s := string(b)
	return &s
}

// decodeIDs reads a stored channel list; NULL is nil, "[]" an empty list.
func (r *Repo) decodeIDs(s *string) []string {
	if s == nil {
		return nil
	}
	ids := []string{}
	if err := json.Unmarshal([]byte(*s), &ids); err != nil {
		r.log.Warn("alerts: undecodable channel_ids", "error", err)
		return []string{}
	}
	return ids
}

// utc is a stored timestamp as UTC.
func utc(dt types.DateTime) time.Time { return dt.Time().UTC() }

// stamp is t as a stored timestamp (UTC, second precision).
func stamp(t time.Time) types.DateTime { return types.DateTime(t.UTC().Truncate(time.Second)) }
