package alert

import (
	"math"
	"time"

	"winterflow/internal/domain/metrics"
)

const (
	// BufferSize is how many minutes of points a target keeps: the
	// longest rule window.
	BufferSize = MaxDuration
	// Coverage is the share of a window's expected one-per-minute points
	// that must exist before its average counts.
	Coverage = 0.8
)

// Point is one sample's alert values, stamped with its minute.
type Point struct {
	At     time.Time
	Values map[Metric]float64
}

// Buffer holds a target's newest points, oldest first. Not safe for
// concurrent use; the evaluator guards it.
type Buffer struct {
	points []Point
}

// Add appends p, stamped with its minute. A minute that is not newer than
// the last one (a replay, a refill overlap) is ignored and Add reports
// false.
func (b *Buffer) Add(p Point) bool {
	p.At = metrics.Tier1.BucketStart(p.At)
	if n := len(b.points); n > 0 && !p.At.After(b.points[n-1].At) {
		return false
	}
	b.points = append(b.points, p)
	if over := len(b.points) - BufferSize; over > 0 {
		b.points = append(b.points[:0:0], b.points[over:]...)
	}
	return true
}

// Len is the number of points held.
func (b *Buffer) Len() int { return len(b.points) }

// Average is m's mean over the points newer than (newest - window). It
// counts only when at least Coverage of the window's expected points carry
// m; ok is false otherwise (no decision).
func (b *Buffer) Average(m Metric, window time.Duration) (avg float64, ok bool) {
	if len(b.points) == 0 || window <= 0 {
		return 0, false
	}
	expected := int(window / time.Minute)
	need := int(math.Ceil(Coverage * float64(expected)))
	if need < 1 {
		need = 1
	}
	cutoff := b.points[len(b.points)-1].At.Add(-window)
	var sum float64
	var n int
	for i := len(b.points) - 1; i >= 0; i-- {
		p := b.points[i]
		if !p.At.After(cutoff) {
			break
		}
		if x, has := p.Values[m]; has {
			sum += x
			n++
		}
	}
	if n < need {
		return 0, false
	}
	return sum / float64(n), true
}
