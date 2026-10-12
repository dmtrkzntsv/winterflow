package alert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error) {
	return sqlitegen.New(db).ListUnseededOrganizationIDs(ctx)
}

func (sqliteQuerier) MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error) {
	return sqlitegen.New(db).MarkAlertDefaultsSeeded(ctx, orgID)
}

func (sqliteQuerier) GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return sqlitegen.New(db).GetAlertServerOrganizationID(ctx, serverID)
}

func (sqliteQuerier) ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error) {
	return sqlitegen.New(db).ListNotificationChannels(ctx, orgID)
}

func (sqliteQuerier) GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error) {
	return sqlitegen.New(db).GetNotificationChannel(ctx, p)
}

func (sqliteQuerier) InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error {
	return sqlitegen.New(db).InsertNotificationChannel(ctx, p)
}

func (sqliteQuerier) UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error) {
	return sqlitegen.New(db).UpdateNotificationChannel(ctx, p)
}

func (sqliteQuerier) DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error) {
	return sqlitegen.New(db).DeleteNotificationChannel(ctx, p)
}

func (sqliteQuerier) RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error {
	return sqlitegen.New(db).RecordChannelSuccess(ctx, p)
}

func (sqliteQuerier) RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error {
	return sqlitegen.New(db).RecordChannelFailure(ctx, p)
}

func (sqliteQuerier) ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error) {
	return sqlitegen.New(db).ListAlertRules(ctx, orgID)
}

func (sqliteQuerier) GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error) {
	return sqlitegen.New(db).GetAlertRule(ctx, p)
}

func (sqliteQuerier) InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error {
	return sqlitegen.New(db).InsertAlertRule(ctx, p)
}

func (sqliteQuerier) UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error) {
	return sqlitegen.New(db).UpdateAlertRule(ctx, p)
}

func (sqliteQuerier) DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertRule(ctx, p)
}

func (sqliteQuerier) ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error) {
	return sqlitegen.New(db).ListAlertOverrides(ctx, orgID)
}

func (sqliteQuerier) ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error) {
	return sqlitegen.New(db).ListTargetAlertOverrides(ctx, p)
}

func (sqliteQuerier) UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error {
	return sqlitegen.New(db).UpsertAlertOverride(ctx, p)
}

func (sqliteQuerier) DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertOverride(ctx, p)
}

func (sqliteQuerier) FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error) {
	return sqlitegen.New(db).FireAlertState(ctx, p)
}

func (sqliteQuerier) ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error) {
	return sqlitegen.New(db).ResolveAlertState(ctx, p)
}

func (sqliteQuerier) DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error {
	return sqlitegen.New(db).DeleteAlertState(ctx, p)
}

func (sqliteQuerier) ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error) {
	return sqlitegen.New(db).ListFiringAlertStates(ctx)
}

func (sqliteQuerier) ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error) {
	return sqlitegen.New(db).ListOrgFiringAlertStates(ctx, orgID)
}

func (sqliteQuerier) CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error) {
	return sqlitegen.New(db).CountFiringAlertsByServer(ctx)
}

func (sqliteQuerier) InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error {
	return sqlitegen.New(db).InsertAlertEvent(ctx, p)
}

func (sqliteQuerier) SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error {
	return sqlitegen.New(db).SetAlertEventDeliveries(ctx, p)
}

func (sqliteQuerier) ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error) {
	return sqlitegen.New(db).ListAlertEvents(ctx, p)
}

func (sqliteQuerier) DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return sqlitegen.New(db).DeleteAlertEventsBefore(ctx, before)
}

func (sqliteQuerier) ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error) {
	return sqlitegen.New(db).ListOversizedAlertOrganizations(ctx)
}

func (sqliteQuerier) GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error) {
	return sqlitegen.New(db).GetAlertEventTrimCutoff(ctx, p)
}

func (sqliteQuerier) DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error) {
	return sqlitegen.New(db).DeleteAlertEventsUpTo(ctx, p)
}

func (sqliteQuerier) ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error) {
	return sqlitegen.New(db).ListAlertDirectoryServers(ctx)
}

func (sqliteQuerier) ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error) {
	return sqlitegen.New(db).ListAlertDirectoryApps(ctx)
}
