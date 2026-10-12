package alert

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) ListUnseededOrganizationIDs(ctx context.Context, db backend.DBTX) ([]string, error) {
	return pgsqlgen.New(db).ListUnseededOrganizationIDs(ctx)
}

func (pgsqlQuerier) MarkAlertDefaultsSeeded(ctx context.Context, db backend.DBTX, orgID string) (int64, error) {
	return pgsqlgen.New(db).MarkAlertDefaultsSeeded(ctx, orgID)
}

func (pgsqlQuerier) GetAlertServerOrganizationID(ctx context.Context, db backend.DBTX, serverID string) (string, error) {
	return pgsqlgen.New(db).GetAlertServerOrganizationID(ctx, serverID)
}

func (pgsqlQuerier) ListNotificationChannels(ctx context.Context, db backend.DBTX, orgID string) ([]channelRow, error) {
	rows, err := pgsqlgen.New(db).ListNotificationChannels(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]channelRow, len(rows))
	for i, row := range rows {
		out[i] = channelRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) GetNotificationChannel(ctx context.Context, db backend.DBTX, p getChannelParams) (channelRow, error) {
	row, err := pgsqlgen.New(db).GetNotificationChannel(ctx, pgsqlgen.GetNotificationChannelParams(p))
	return channelRow(row), err
}

func (pgsqlQuerier) InsertNotificationChannel(ctx context.Context, db backend.DBTX, p insertChanParams) error {
	return pgsqlgen.New(db).InsertNotificationChannel(ctx, pgsqlgen.InsertNotificationChannelParams(p))
}

func (pgsqlQuerier) UpdateNotificationChannel(ctx context.Context, db backend.DBTX, p updateChanParams) (int64, error) {
	return pgsqlgen.New(db).UpdateNotificationChannel(ctx, pgsqlgen.UpdateNotificationChannelParams(p))
}

func (pgsqlQuerier) DeleteNotificationChannel(ctx context.Context, db backend.DBTX, p deleteChanParams) (int64, error) {
	return pgsqlgen.New(db).DeleteNotificationChannel(ctx, pgsqlgen.DeleteNotificationChannelParams(p))
}

func (pgsqlQuerier) RecordChannelSuccess(ctx context.Context, db backend.DBTX, p successParams) error {
	return pgsqlgen.New(db).RecordChannelSuccess(ctx, pgsqlgen.RecordChannelSuccessParams(p))
}

func (pgsqlQuerier) RecordChannelFailure(ctx context.Context, db backend.DBTX, p failureParams) error {
	return pgsqlgen.New(db).RecordChannelFailure(ctx, pgsqlgen.RecordChannelFailureParams(p))
}

func (pgsqlQuerier) ListAlertRules(ctx context.Context, db backend.DBTX, orgID string) ([]ruleRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertRules(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]ruleRow, len(rows))
	for i, row := range rows {
		out[i] = ruleRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) GetAlertRule(ctx context.Context, db backend.DBTX, p getRuleParams) (ruleRow, error) {
	row, err := pgsqlgen.New(db).GetAlertRule(ctx, pgsqlgen.GetAlertRuleParams(p))
	return ruleRow(row), err
}

func (pgsqlQuerier) InsertAlertRule(ctx context.Context, db backend.DBTX, p insertRuleParams) error {
	return pgsqlgen.New(db).InsertAlertRule(ctx, pgsqlgen.InsertAlertRuleParams(p))
}

func (pgsqlQuerier) UpdateAlertRule(ctx context.Context, db backend.DBTX, p updateRuleParams) (int64, error) {
	return pgsqlgen.New(db).UpdateAlertRule(ctx, pgsqlgen.UpdateAlertRuleParams(p))
}

func (pgsqlQuerier) DeleteAlertRule(ctx context.Context, db backend.DBTX, p deleteRuleParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertRule(ctx, pgsqlgen.DeleteAlertRuleParams(p))
}

func (pgsqlQuerier) ListAlertOverrides(ctx context.Context, db backend.DBTX, orgID string) ([]overrideRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertOverrides(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]overrideRow, len(rows))
	for i, row := range rows {
		out[i] = overrideRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListTargetAlertOverrides(ctx context.Context, db backend.DBTX, p targetOvrParams) ([]overrideRow, error) {
	rows, err := pgsqlgen.New(db).ListTargetAlertOverrides(ctx, pgsqlgen.ListTargetAlertOverridesParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]overrideRow, len(rows))
	for i, row := range rows {
		out[i] = overrideRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) UpsertAlertOverride(ctx context.Context, db backend.DBTX, p upsertOvrParams) error {
	return pgsqlgen.New(db).UpsertAlertOverride(ctx, pgsqlgen.UpsertAlertOverrideParams(p))
}

func (pgsqlQuerier) DeleteAlertOverride(ctx context.Context, db backend.DBTX, p deleteOvrParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertOverride(ctx, pgsqlgen.DeleteAlertOverrideParams(p))
}

func (pgsqlQuerier) FireAlertState(ctx context.Context, db backend.DBTX, p fireParams) (int64, error) {
	return pgsqlgen.New(db).FireAlertState(ctx, pgsqlgen.FireAlertStateParams(p))
}

func (pgsqlQuerier) ResolveAlertState(ctx context.Context, db backend.DBTX, p resolveParams) (int64, error) {
	return pgsqlgen.New(db).ResolveAlertState(ctx, pgsqlgen.ResolveAlertStateParams(p))
}

func (pgsqlQuerier) DeleteAlertState(ctx context.Context, db backend.DBTX, p deleteStateParam) error {
	return pgsqlgen.New(db).DeleteAlertState(ctx, pgsqlgen.DeleteAlertStateParams(p))
}

func (pgsqlQuerier) ListFiringAlertStates(ctx context.Context, db backend.DBTX) ([]firingRow, error) {
	rows, err := pgsqlgen.New(db).ListFiringAlertStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]firingRow, len(rows))
	for i, row := range rows {
		out[i] = firingRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListOrgFiringAlertStates(ctx context.Context, db backend.DBTX, orgID string) ([]orgFiringRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgFiringAlertStates(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]orgFiringRow, len(rows))
	for i, row := range rows {
		out[i] = orgFiringRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) CountFiringAlertsByServer(ctx context.Context, db backend.DBTX) ([]countRow, error) {
	rows, err := pgsqlgen.New(db).CountFiringAlertsByServer(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]countRow, len(rows))
	for i, row := range rows {
		out[i] = countRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) InsertAlertEvent(ctx context.Context, db backend.DBTX, p insertEventParam) error {
	return pgsqlgen.New(db).InsertAlertEvent(ctx, pgsqlgen.InsertAlertEventParams(p))
}

func (pgsqlQuerier) SetAlertEventDeliveries(ctx context.Context, db backend.DBTX, p deliveriesParams) error {
	return pgsqlgen.New(db).SetAlertEventDeliveries(ctx, pgsqlgen.SetAlertEventDeliveriesParams(p))
}

func (pgsqlQuerier) ListAlertEvents(ctx context.Context, db backend.DBTX, p listEventsParams) ([]eventRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertEvents(ctx, pgsqlgen.ListAlertEventsParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]eventRow, len(rows))
	for i, row := range rows {
		out[i] = eventRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteAlertEventsBefore(ctx context.Context, db backend.DBTX, before types.DateTime) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertEventsBefore(ctx, before)
}

func (pgsqlQuerier) ListOversizedAlertOrganizations(ctx context.Context, db backend.DBTX) ([]string, error) {
	return pgsqlgen.New(db).ListOversizedAlertOrganizations(ctx)
}

func (pgsqlQuerier) GetAlertEventTrimCutoff(ctx context.Context, db backend.DBTX, p cutoffParams) (cutoffRow, error) {
	row, err := pgsqlgen.New(db).GetAlertEventTrimCutoff(ctx, pgsqlgen.GetAlertEventTrimCutoffParams(p))
	return cutoffRow(row), err
}

func (pgsqlQuerier) DeleteAlertEventsUpTo(ctx context.Context, db backend.DBTX, p deleteUpToParams) (int64, error) {
	return pgsqlgen.New(db).DeleteAlertEventsUpTo(ctx, pgsqlgen.DeleteAlertEventsUpToParams(p))
}

func (pgsqlQuerier) ListAlertDirectoryServers(ctx context.Context, db backend.DBTX) ([]dirServerRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertDirectoryServers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dirServerRow, len(rows))
	for i, row := range rows {
		out[i] = dirServerRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListAlertDirectoryApps(ctx context.Context, db backend.DBTX) ([]dirAppRow, error) {
	rows, err := pgsqlgen.New(db).ListAlertDirectoryApps(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dirAppRow, len(rows))
	for i, row := range rows {
		out[i] = dirAppRow(row)
	}
	return out, nil
}
