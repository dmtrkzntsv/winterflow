package dockercompose

import (
	"sort"
	"strconv"
)

// parseRevisions converts a list of revision directory names into sorted,
// ascending revision numbers, skipping any non-numeric entries.
func parseRevisions(names []string) []uint32 {
	out := make([]uint32, 0, len(names))
	for _, n := range names {
		v, err := strconv.ParseUint(n, 10, 32)
		if err != nil {
			continue
		}
		out = append(out, uint32(v))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nextRevision returns the revision number to assign to a new save: one past
// the current maximum, or 1 if there are none.
func nextRevision(existing []uint32) uint32 {
	if len(existing) == 0 {
		return 1
	}
	return existing[len(existing)-1] + 1
}

// revisionsToPrune returns the revision numbers that should be deleted to keep
// at most keep revisions, oldest first. Input need not be sorted.
func revisionsToPrune(existing []uint32, keep int) []uint32 {
	if keep <= 0 || len(existing) <= keep {
		return nil
	}
	sorted := append([]uint32(nil), existing...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[:len(sorted)-keep]
}
