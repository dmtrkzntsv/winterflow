package alert

import (
	"context"
	"time"

	"winterflow/internal/domain/alert"
)

// ListRules returns an org's rules: server rules first, then by metric
// and threshold.
func (r *Repo) ListRules(ctx context.Context, orgID string) ([]alert.Rule, error) {
	rows, err := r.q.ListAlertRules(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]alert.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, r.rule(row))
	}
	return out, nil
}

// GetRule returns one of the org's rules or alert.ErrNotFound.
func (r *Repo) GetRule(ctx context.Context, orgID, id string) (alert.Rule, error) {
	row, err := r.q.GetAlertRule(ctx, r.tx.Querier(ctx), getRuleParams{OrganizationID: orgID, RuleID: id})
	if err != nil {
		return alert.Rule{}, notFound(err)
	}
	return r.rule(row), nil
}

// CreateRule stores a new rule, minting its id and creation time.
func (r *Repo) CreateRule(ctx context.Context, rule alert.Rule) (alert.Rule, error) {
	rule.ID = r.newID()
	rule.CreatedAt = time.Now().UTC().Truncate(time.Second)
	return rule, r.insertRule(ctx, rule)
}

func (r *Repo) insertRule(ctx context.Context, rule alert.Rule) error {
	return r.q.InsertAlertRule(ctx, r.tx.Querier(ctx), insertRuleParams{
		RuleID: rule.ID, OrganizationID: rule.OrgID, TargetKind: string(rule.TargetKind), Metric: string(rule.Metric),
		Threshold: rule.Threshold, DurationMinutes: int64(rule.DurationMinutes), ChannelIds: encodeIDs(rule.ChannelIDs),
		Enabled: rule.Enabled, CreatedAt: stamp(rule.CreatedAt),
	})
}

// UpdateRule rewrites a rule's threshold, duration, channels and enabled
// flag (kind and metric are fixed); alert.ErrNotFound when the org has no
// such rule.
func (r *Repo) UpdateRule(ctx context.Context, rule alert.Rule) error {
	n, err := r.q.UpdateAlertRule(ctx, r.tx.Querier(ctx), updateRuleParams{
		Threshold: rule.Threshold, DurationMinutes: int64(rule.DurationMinutes), ChannelIds: encodeIDs(rule.ChannelIDs),
		Enabled: rule.Enabled, OrganizationID: rule.OrgID, RuleID: rule.ID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// DeleteRule removes a rule; its overrides and states cascade, its
// history stays.
func (r *Repo) DeleteRule(ctx context.Context, orgID, id string) error {
	n, err := r.q.DeleteAlertRule(ctx, r.tx.Querier(ctx), deleteRuleParams{OrganizationID: orgID, RuleID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// SeedDefaults gives every org that has not had them yet the default rules
// (alert.DefaultRules) and reports how many orgs it seeded. Marking the
// org and inserting its rules share one transaction, and the mark is
// compare-and-set, so concurrent API replicas seed an org once. The flag
// also makes deleting a default permanent.
func (r *Repo) SeedDefaults(ctx context.Context) (int, error) {
	orgs, err := r.q.ListUnseededOrganizationIDs(ctx, r.tx.Querier(ctx))
	if err != nil {
		return 0, err
	}
	seeded := 0
	for _, orgID := range orgs {
		err := r.tx.WithTx(ctx, func(ctx context.Context) error {
			n, err := r.q.MarkAlertDefaultsSeeded(ctx, r.tx.Querier(ctx), orgID)
			if err != nil || n == 0 {
				return err
			}
			now := time.Now().UTC().Truncate(time.Second)
			for _, rule := range alert.DefaultRules() {
				rule.ID, rule.OrgID, rule.CreatedAt = r.newID(), orgID, now
				if err := r.insertRule(ctx, rule); err != nil {
					return err
				}
			}
			seeded++
			return nil
		})
		if err != nil {
			return seeded, err
		}
	}
	return seeded, nil
}

// ServerOrgID returns the org that owns a server, or alert.ErrNotFound.
func (r *Repo) ServerOrgID(ctx context.Context, serverID string) (string, error) {
	org, err := r.q.GetAlertServerOrganizationID(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return "", notFound(err)
	}
	return org, nil
}

// ListOverrides returns every override of the org's rules.
func (r *Repo) ListOverrides(ctx context.Context, orgID string) ([]alert.Override, error) {
	rows, err := r.q.ListAlertOverrides(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	return r.overrides(rows), nil
}

// TargetOverrides returns the overrides of one target, by rule.
func (r *Repo) TargetOverrides(ctx context.Context, t alert.Target) ([]alert.Override, error) {
	rows, err := r.q.ListTargetAlertOverrides(ctx, r.tx.Querier(ctx), targetOvrParams{ServerID: t.ServerID, AppID: t.AppID})
	if err != nil {
		return nil, err
	}
	return r.overrides(rows), nil
}

// SaveOverride creates or replaces a (rule, target) override.
func (r *Repo) SaveOverride(ctx context.Context, o alert.Override) error {
	var dur *int64
	if o.DurationMinutes != nil {
		d := int64(*o.DurationMinutes)
		dur = &d
	}
	return r.q.UpsertAlertOverride(ctx, r.tx.Querier(ctx), upsertOvrParams{
		RuleID: o.RuleID, ServerID: o.ServerID, AppID: o.AppID, Disabled: o.Disabled,
		Threshold: o.Threshold, DurationMinutes: dur, ChannelIds: encodeIDs(o.ChannelIDs),
	})
}

// DeleteOverride removes a (rule, target) override; deleting one that does
// not exist is not an error.
func (r *Repo) DeleteOverride(ctx context.Context, k alert.Key) error {
	_, err := r.q.DeleteAlertOverride(ctx, r.tx.Querier(ctx), deleteOvrParams{RuleID: k.RuleID, ServerID: k.ServerID, AppID: k.AppID})
	return err
}

func (r *Repo) rule(row ruleRow) alert.Rule {
	return alert.Rule{
		ID: row.RuleID, OrgID: row.OrganizationID, TargetKind: alert.TargetKind(row.TargetKind),
		Metric: alert.Metric(row.Metric), Threshold: row.Threshold, DurationMinutes: int(row.DurationMinutes),
		ChannelIDs: r.decodeIDs(row.ChannelIds), Enabled: row.Enabled, CreatedAt: utc(row.CreatedAt),
	}
}

func (r *Repo) overrides(rows []overrideRow) []alert.Override {
	out := make([]alert.Override, 0, len(rows))
	for _, row := range rows {
		o := alert.Override{
			RuleID: row.RuleID, ServerID: row.ServerID, AppID: row.AppID, Disabled: row.Disabled,
			Threshold: row.Threshold, ChannelIDs: r.decodeIDs(row.ChannelIds),
		}
		if row.DurationMinutes != nil {
			d := int(*row.DurationMinutes)
			o.DurationMinutes = &d
		}
		out = append(out, o)
	}
	return out
}
