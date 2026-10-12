package metrics

import "time"

// Tier is a storage resolution in minutes. Tier1 rows are raw samples; each
// higher tier is rolled up from the one before it.
type Tier int

const (
	Tier1   Tier = 1
	Tier10  Tier = 10
	Tier20  Tier = 20
	Tier120 Tier = 120
	Tier480 Tier = 480
)

// Tiers lists every tier, finest first.
var Tiers = []Tier{Tier1, Tier10, Tier20, Tier120, Tier480}

// RollupPair is one rollup step: closed To-buckets are built from From rows.
type RollupPair struct{ From, To Tier }

// RollupPairs are the rollup steps, in the order they must run.
var RollupPairs = []RollupPair{{Tier1, Tier10}, {Tier10, Tier20}, {Tier20, Tier120}, {Tier120, Tier480}}

// RollupGrace keeps a bucket open this long after its end, so a sample still
// in flight on the bus lands before the bucket is rolled up.
const RollupGrace = 2 * time.Minute

// Duration is the bucket length.
func (t Tier) Duration() time.Duration { return time.Duration(t) * time.Minute }

// Retention is how long rows of the tier are kept.
func (t Tier) Retention() time.Duration {
	switch t {
	case Tier1:
		return time.Hour
	case Tier10:
		return 12 * time.Hour
	case Tier20:
		return 24 * time.Hour
	case Tier120:
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

// BucketStart aligns t (in UTC) down to the tier's bucket.
func (t Tier) BucketStart(at time.Time) time.Time {
	return at.UTC().Truncate(t.Duration())
}

// Range is a chart range the API accepts.
type Range string

const (
	Range1h  Range = "1h"
	Range12h Range = "12h"
	Range24h Range = "24h"
	Range7d  Range = "7d"
	Range30d Range = "30d"
)

// ParseRange maps a chart range to the tier that serves it and the span it
// covers; ok is false for an unknown range.
func ParseRange(s string) (tier Tier, span time.Duration, ok bool) {
	switch Range(s) {
	case Range1h:
		return Tier1, time.Hour, true
	case Range12h:
		return Tier10, 12 * time.Hour, true
	case Range24h:
		return Tier20, 24 * time.Hour, true
	case Range7d:
		return Tier120, 7 * 24 * time.Hour, true
	case Range30d:
		return Tier480, 30 * 24 * time.Hour, true
	}
	return 0, 0, false
}
