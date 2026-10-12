package metrics

import (
	"math"
	"strings"
	"time"
)

// Bucket is one stored row: its start, how many raw samples it stands for,
// and its values.
type Bucket struct {
	Start   time.Time
	Samples int
	Data    Values
}

// TargetBucket is one stored row with its target: the server (AppID "")
// or one of its apps.
type TargetBucket struct {
	ServerID string
	AppID    string
	Bucket
}

// UnavailableField is the app flag set when container metrics cannot be
// read on the host. Rollup averages it over every row (absent = 0), so a
// rolled-up value is the flagged fraction of the bucket.
const UnavailableField = "unavailable"

// Rollup merges the rows of one target that fall into the destination bucket
// starting at start. Every field gets its average weighted by the rows'
// sample counts (over the rows that carry the field; UnavailableField over all rows) and field+"_max": the
// largest of the rows' peaks, where a row without a peak (tier 1) counts its
// value. Samples is the sum of the rows' counts.
func Rollup(start time.Time, rows []Bucket) Bucket {
	type acc struct{ sum, weight, max float64 }
	accs := map[string]*acc{}
	total := 0
	for _, r := range rows {
		n := r.Samples
		if n < 1 {
			n = 1
		}
		total += n
		for k, x := range r.Data {
			if strings.HasSuffix(k, MaxSuffix) {
				continue
			}
			peak := x
			if m, ok := r.Data[k+MaxSuffix]; ok {
				peak = m
			}
			a := accs[k]
			if a == nil {
				a = &acc{max: math.Inf(-1)}
				accs[k] = a
			}
			a.sum += x * float64(n)
			a.weight += float64(n)
			a.max = math.Max(a.max, peak)
		}
	}
	out := Values{}
	for k, a := range accs {
		if k == UnavailableField {
			a.weight = float64(total) // a flag: absent means 0
		}
		out.put(k, a.sum/a.weight)
		out.put(k+MaxSuffix, a.max)
	}
	return Bucket{Start: start.UTC(), Samples: total, Data: out}
}

// Point is one chart point: "t" (the bucket start, unix seconds), every
// field's average, and every field's "<field>_max" peak.
type Point map[string]float64

// ToPoint renders a stored row as a chart point. Tier-1 rows carry no peaks;
// their peak is the value itself, so every point has both.
func ToPoint(b Bucket) Point {
	p := Point{"t": float64(b.Start.Unix())}
	for k, x := range b.Data {
		p[k] = x
	}
	for k, x := range b.Data {
		if strings.HasSuffix(k, MaxSuffix) {
			continue
		}
		if _, ok := b.Data[k+MaxSuffix]; !ok {
			p[k+MaxSuffix] = x
		}
	}
	return p
}

// Pick keeps "t" and the named fields with their peaks.
func (p Point) Pick(fields ...string) Point {
	out := Point{"t": p["t"]}
	for _, f := range fields {
		if x, ok := p[f]; ok {
			out[f] = x
		}
		if x, ok := p[f+MaxSuffix]; ok {
			out[f+MaxSuffix] = x
		}
	}
	return out
}
