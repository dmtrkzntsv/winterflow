package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"winterflow/internal/domain/alert"
)

const (
	// EventRetention is how long history is kept.
	EventRetention = 90 * 24 * time.Hour
	// EventsPerOrg caps an org's history. ListOversizedAlertOrganizations
	// (query/*/alerts.sql) repeats this literal: keep them in step.
	EventsPerOrg = 2000
	// MaxEventsPage bounds one history page.
	MaxEventsPage = 200
)

// farFuture starts the first history page.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

func (r *Repo) insertEvent(ctx context.Context, ev alert.Event) error {
	return r.q.InsertAlertEvent(ctx, r.tx.Querier(ctx), insertEventParam{
		EventID: ev.ID, OrganizationID: ev.OrgID, RuleID: ev.RuleID, ServerID: ev.ServerID, AppID: ev.AppID,
		ServerName: ev.ServerName, AppName: ev.AppName, TargetKind: string(ev.TargetKind), Metric: string(ev.Metric),
		Kind: string(ev.Kind), Value: ev.Value, Threshold: ev.Threshold, Title: ev.Title, Message: ev.Message,
		CreatedAt: stamp(ev.CreatedAt),
	})
}

// ListEvents returns one history page, newest first. A zero BeforeAt
// starts at the newest event; Limit is clamped to 1..MaxEventsPage.
func (r *Repo) ListEvents(ctx context.Context, q alert.EventQuery) ([]alert.Event, error) {
	before := farFuture
	if !q.BeforeAt.IsZero() {
		before = q.BeforeAt
	}
	limit := min(max(q.Limit, 1), MaxEventsPage)
	rows, err := r.q.ListAlertEvents(ctx, r.tx.Querier(ctx), listEventsParams{
		OrganizationID: q.OrgID, ServerID: q.ServerID, AppID: q.AppID, RuleID: q.RuleID, Kind: string(q.Kind),
		BeforeAt: stamp(before), BeforeID: q.BeforeID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]alert.Event, 0, len(rows))
	for _, row := range rows {
		ev := alert.Event{
			ID: row.EventID, OrgID: row.OrganizationID, RuleID: row.RuleID, ServerID: row.ServerID, AppID: row.AppID,
			ServerName: row.ServerName, AppName: row.AppName, TargetKind: alert.TargetKind(row.TargetKind),
			Metric: alert.Metric(row.Metric), Kind: alert.EventKind(row.Kind), Value: row.Value, Threshold: row.Threshold,
			Title: row.Title, Message: row.Message, Deliveries: []alert.Delivery{}, CreatedAt: utc(row.CreatedAt),
		}
		if err := json.Unmarshal([]byte(row.Deliveries), &ev.Deliveries); err != nil {
			r.log.Warn("alerts: undecodable deliveries", "event_id", row.EventID, "error", err)
			ev.Deliveries = []alert.Delivery{}
		}
		out = append(out, ev)
	}
	return out, nil
}

// SetDeliveries records an event's per-channel outcomes.
func (r *Repo) SetDeliveries(ctx context.Context, eventID string, ds []alert.Delivery) error {
	if ds == nil {
		ds = []alert.Delivery{}
	}
	b, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	return r.q.SetAlertEventDeliveries(ctx, r.tx.Querier(ctx), deliveriesParams{Deliveries: string(b), EventID: eventID})
}

// PruneEvents deletes history older than EventRetention, then trims every
// org past EventsPerOrg to its newest EventsPerOrg; it reports the rows
// deleted.
func (r *Repo) PruneEvents(ctx context.Context, now time.Time) (int64, error) {
	total, err := r.q.DeleteAlertEventsBefore(ctx, r.tx.Querier(ctx), stamp(now.Add(-EventRetention)))
	if err != nil {
		return 0, err
	}
	orgs, err := r.q.ListOversizedAlertOrganizations(ctx, r.tx.Querier(ctx))
	if err != nil {
		return total, err
	}
	for _, orgID := range orgs {
		cut, err := r.q.GetAlertEventTrimCutoff(ctx, r.tx.Querier(ctx), cutoffParams{OrganizationID: orgID, Keep: EventsPerOrg})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return total, err
		}
		n, err := r.q.DeleteAlertEventsUpTo(ctx, r.tx.Querier(ctx), deleteUpToParams{
			OrganizationID: orgID, CutoffAt: cut.CreatedAt, CutoffID: cut.EventID,
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
