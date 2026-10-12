package alert

import (
	"context"

	"winterflow/internal/domain/alert"
)

// Fire moves k to firing and, if this call made the transition, appends
// ev in the same transaction. won is false when k was already firing
// (another API replica got there first, or a replay).
func (r *Repo) Fire(ctx context.Context, k alert.Key, value, threshold float64, ev alert.Event) (won bool, err error) {
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.FireAlertState(ctx, r.tx.Querier(ctx), fireParams{
			RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID,
			Since: stamp(ev.CreatedAt), Value: value, Threshold: threshold,
		})
		if err != nil || n == 0 {
			return err
		}
		won = true
		return r.insertEvent(ctx, ev)
	})
	return won, err
}

// Resolve moves k from firing to ok and, if this call made the
// transition, appends ev in the same transaction.
func (r *Repo) Resolve(ctx context.Context, k alert.Key, value float64, ev alert.Event) (won bool, err error) {
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.ResolveAlertState(ctx, r.tx.Querier(ctx), resolveParams{
			Since: stamp(ev.CreatedAt), Value: value, RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID,
		})
		if err != nil || n == 0 {
			return err
		}
		won = true
		return r.insertEvent(ctx, ev)
	})
	return won, err
}

// ClearState forgets k's state without an event (the rule or override was
// disabled while firing).
func (r *Repo) ClearState(ctx context.Context, k alert.Key) error {
	return r.q.DeleteAlertState(ctx, r.tx.Querier(ctx), deleteStateParam{RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID})
}

// FiringStates returns every firing (rule, target) with its rule's kind and
// metric: the evaluator's state after a restart.
func (r *Repo) FiringStates(ctx context.Context) ([]alert.StateRow, error) {
	rows, err := r.q.ListFiringAlertStates(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make([]alert.StateRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, alert.StateRow{
			Key:        alert.Key{RuleID: row.RuleID, Target: alert.Target{ServerID: row.ServerID, AppID: row.AppID}},
			TargetKind: alert.TargetKind(row.TargetKind), Metric: alert.Metric(row.Metric), State: alert.StateFiring,
			Since: utc(row.Since), Value: row.Value, Threshold: row.Threshold,
		})
	}
	return out, nil
}

// ListActive returns an org's firing alerts of enabled rules with their
// target names, newest first.
func (r *Repo) ListActive(ctx context.Context, orgID string) ([]alert.Active, error) {
	rows, err := r.q.ListOrgFiringAlertStates(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Active, 0, len(rows))
	for _, row := range rows {
		out = append(out, alert.Active{
			RuleID: row.RuleID, TargetKind: alert.TargetKind(row.TargetKind), Metric: alert.Metric(row.Metric),
			ServerID: row.ServerID, AppID: row.AppID, Since: utc(row.Since), Value: row.Value, Threshold: row.Threshold,
			ServerName: row.ServerName, AppName: row.AppName,
		})
	}
	return out, nil
}

// CountFiring returns the number of firing alerts of enabled rules per
// server (servers without any are absent).
func (r *Repo) CountFiring(ctx context.Context) (map[string]int, error) {
	rows, err := r.q.CountFiringAlertsByServer(ctx, r.tx.Querier(ctx))
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, row := range rows {
		out[row.ServerID] = int(row.Firing)
	}
	return out, nil
}

// Directory returns every server and app id with its name and owner: the
// evaluator's targets.
func (r *Repo) Directory(ctx context.Context) (alert.Directory, error) {
	servers, err := r.q.ListAlertDirectoryServers(ctx, r.tx.Querier(ctx))
	if err != nil {
		return alert.Directory{}, err
	}
	apps, err := r.q.ListAlertDirectoryApps(ctx, r.tx.Querier(ctx))
	if err != nil {
		return alert.Directory{}, err
	}
	d := alert.Directory{
		Servers: make(map[string]alert.ServerRef, len(servers)),
		Apps:    make(map[string]alert.AppRef, len(apps)),
	}
	for _, s := range servers {
		d.Servers[s.ServerID] = alert.ServerRef{ID: s.ServerID, OrgID: s.OrganizationID, Name: s.Name}
	}
	for _, a := range apps {
		d.Apps[a.AppID] = alert.AppRef{ID: a.AppID, ServerID: a.ServerID, Name: a.Name}
	}
	return d, nil
}
