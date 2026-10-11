package port

import (
	"context"
	"time"

	"winterflow/internal/domain/metrics"
)

// MetricRepository stores monitoring samples (metric_samples): tier-1 rows
// written once a minute per server and app, rolled up into coarser tiers and
// pruned per tier. Every write is insert-or-ignore on (server, app, tier,
// bucket), so replays and several API replicas are harmless.
type MetricRepository interface {
	// InsertSample writes one tier-1 row for the server (app "") and one per
	// app in apps, in one transaction, at at truncated to the minute. Apps
	// the API does not know on that server are skipped.
	InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error
	// Series returns one target's rows of tier since from, oldest first
	// (appID "" is the server itself).
	Series(ctx context.Context, serverID, appID string, tier metrics.Tier, from time.Time) ([]metrics.Bucket, error)
	// AppsSeries returns every app's rows of tier since from, keyed by app.
	AppsSeries(ctx context.Context, serverID string, tier metrics.Tier, from time.Time) (map[string][]metrics.Bucket, error)
	// Rollup builds every closed, missing bucket of each coarser tier from
	// the tier below and reports how many rows it wrote.
	Rollup(ctx context.Context, now time.Time) (int, error)
	// Prune deletes rows past their tier's retention and reports how many.
	Prune(ctx context.Context, now time.Time) (int64, error)
}
