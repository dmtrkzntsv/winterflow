package metric

import (
	"context"
	"sort"
	"time"

	"winterflow/internal/domain/metrics"
	"winterflow/internal/infra/db/types"
)

// target is one rolled-up row's identity.
type target struct {
	serverID, appID string
	start           time.Time
}

// Rollup runs every rollup step, finest first, so one call can carry a
// sample all the way from tier 1 to tier 480 once the buckets are closed.
func (r *Repo) Rollup(ctx context.Context, now time.Time) (int, error) {
	total := 0
	for _, p := range metrics.RollupPairs {
		n, err := r.rollupPair(ctx, p, now)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// rollupPair writes every closed p.To bucket that has p.From rows and no
// row of its own yet. The window starts at the source tier's retention
// (older source rows are gone) and ends at the last bucket closed for
// RollupGrace. It runs in one transaction; rows of apps deleted meanwhile
// are not written.
func (r *Repo) rollupPair(ctx context.Context, p metrics.RollupPair, now time.Time) (int, error) {
	upper := p.To.BucketStart(now.Add(-metrics.RollupGrace))
	lower := p.To.BucketStart(now.Add(-p.From.Retention()))
	if !upper.After(lower) {
		return 0, nil
	}
	written := 0
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		q := r.tx.Querier(ctx)
		window := rangeParams{FromTime: types.DateTime(lower), ToTime: types.DateTime(upper)}

		window.Tier = int64(p.To)
		done, err := r.q.ListMetricSamplesInRange(ctx, q, window)
		if err != nil {
			return err
		}
		have := make(map[target]bool, len(done))
		for _, row := range done {
			have[target{row.ServerID, row.AppID, row.BucketStart.Time().UTC()}] = true
		}

		window.Tier = int64(p.From)
		src, err := r.q.ListMetricSamplesInRange(ctx, q, window)
		if err != nil {
			return err
		}
		groups := map[target][]metrics.Bucket{}
		for _, row := range src {
			k := target{row.ServerID, row.AppID, p.To.BucketStart(row.BucketStart.Time())}
			if have[k] {
				continue
			}
			if b, ok := r.hydrate(row); ok {
				groups[k] = append(groups[k], b)
			}
		}

		keys := make([]target, 0, len(groups))
		for k := range groups {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i], keys[j]
			if a.serverID != b.serverID {
				return a.serverID < b.serverID
			}
			if a.appID != b.appID {
				return a.appID < b.appID
			}
			return a.start.Before(b.start)
		})

		apps := map[string]map[string]bool{} // server -> existing app ids
		for _, k := range keys {
			if k.appID != "" {
				known, ok := apps[k.serverID]
				if !ok {
					ids, err := r.q.ListAppIDsByServer(ctx, q, k.serverID)
					if err != nil {
						return err
					}
					known = make(map[string]bool, len(ids))
					for _, id := range ids {
						known[id] = true
					}
					apps[k.serverID] = known
				}
				if !known[k.appID] {
					continue
				}
			}
			b := metrics.Rollup(k.start, groups[k])
			if err := r.insert(ctx, q, k.serverID, k.appID, p.To, types.DateTime(b.Start), b.Samples, b.Data); err != nil {
				return err
			}
			written++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return written, nil
}
