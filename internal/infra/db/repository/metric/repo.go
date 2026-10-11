// Package metric implements port.MetricRepository over metric_samples. The
// rollup math lives in the domain (metrics.Rollup) so SQLite and Postgres
// produce identical rows; SQL here only selects, inserts and deletes.
package metric

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	sampleRow    = sqlitegen.MetricSample
	insertParams = sqlitegen.InsertMetricSampleParams
	listParams   = sqlitegen.ListMetricSamplesParams
	appsParams   = sqlitegen.ListServerAppMetricSamplesParams
	pruneParams  = sqlitegen.DeleteMetricSamplesBeforeParams
	rangeParams  = sqlitegen.ListMetricSamplesInRangeParams
)

type querier interface {
	InsertMetricSample(ctx context.Context, db backend.DBTX, p insertParams) error
	ListMetricSamples(ctx context.Context, db backend.DBTX, p listParams) ([]sampleRow, error)
	ListServerAppMetricSamples(ctx context.Context, db backend.DBTX, p appsParams) ([]sampleRow, error)
	DeleteMetricSamplesBefore(ctx context.Context, db backend.DBTX, p pruneParams) (int64, error)
	ListMetricSamplesInRange(ctx context.Context, db backend.DBTX, p rangeParams) ([]sampleRow, error)
	ListAppIDsByServer(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
}

// Repo is the metric_samples repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.MetricRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("metric repository: unknown database driver %q", driver)
}

// InsertSample writes the server row and the known apps' rows at tier 1. An
// app with no values (a stopped one) gets no row.
func (r *Repo) InsertSample(ctx context.Context, serverID string, at time.Time, server metrics.Values, apps map[string]metrics.Values) error {
	bucket := types.DateTime(metrics.Tier1.BucketStart(at))
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		if err := r.insert(ctx, q, serverID, "", metrics.Tier1, bucket, 1, server); err != nil {
			return err
		}
		if len(apps) == 0 {
			return nil
		}
		known, err := r.q.ListAppIDsByServer(ctx, q, serverID)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(known))
		for _, id := range known {
			if v, ok := apps[id]; ok && len(v) > 0 {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := r.insert(ctx, q, serverID, id, metrics.Tier1, bucket, 1, apps[id]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) insert(ctx context.Context, q backend.DBTX, serverID, appID string, tier metrics.Tier, bucket types.DateTime, samples int, v metrics.Values) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return r.q.InsertMetricSample(ctx, q, insertParams{
		ServerID: serverID, AppID: appID, Tier: int64(tier), BucketStart: bucket,
		Samples: int64(samples), Data: string(data),
	})
}

// Series returns one target's rows since from, oldest first.
func (r *Repo) Series(ctx context.Context, serverID, appID string, tier metrics.Tier, from time.Time) ([]metrics.Bucket, error) {
	rows, err := r.q.ListMetricSamples(ctx, r.tx.Querier(ctx), listParams{
		ServerID: serverID, AppID: appID, Tier: int64(tier), BucketStart: types.DateTime(from.UTC()),
	})
	if err != nil {
		return nil, err
	}
	out := make([]metrics.Bucket, 0, len(rows))
	for _, row := range rows {
		b, ok := r.hydrate(row)
		if ok {
			out = append(out, b)
		}
	}
	return out, nil
}

// AppsSeries returns every app's rows since from, keyed by app id.
func (r *Repo) AppsSeries(ctx context.Context, serverID string, tier metrics.Tier, from time.Time) (map[string][]metrics.Bucket, error) {
	rows, err := r.q.ListServerAppMetricSamples(ctx, r.tx.Querier(ctx), appsParams{
		ServerID: serverID, Tier: int64(tier), BucketStart: types.DateTime(from.UTC()),
	})
	if err != nil {
		return nil, err
	}
	out := map[string][]metrics.Bucket{}
	for _, row := range rows {
		if b, ok := r.hydrate(row); ok {
			out[row.AppID] = append(out[row.AppID], b)
		}
	}
	return out, nil
}

// Prune deletes every tier's rows older than its retention.
func (r *Repo) Prune(ctx context.Context, now time.Time) (int64, error) {
	var total int64
	for _, tier := range metrics.Tiers {
		n, err := r.q.DeleteMetricSamplesBefore(ctx, r.tx.Querier(ctx), pruneParams{
			Tier: int64(tier), BucketStart: types.DateTime(now.UTC().Add(-tier.Retention())),
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// hydrate decodes a row; a row whose data is not JSON is logged and skipped
// rather than failing the whole chart.
func (r *Repo) hydrate(row sampleRow) (metrics.Bucket, bool) {
	var v metrics.Values
	if err := json.Unmarshal([]byte(row.Data), &v); err != nil {
		r.log.Warn("metric_samples: undecodable data", "server_id", row.ServerID, "app_id", row.AppID, "error", err)
		return metrics.Bucket{}, false
	}
	return metrics.Bucket{Start: row.BucketStart.Time().UTC(), Samples: int(row.Samples), Data: v}, true
}
