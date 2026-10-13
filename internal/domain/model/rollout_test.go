package model

import (
	"slices"
	"testing"
	"time"
)

func TestReloadScopeMerge(t *testing.T) {
	a := ReloadScope{Names: []string{"B", "A"}, ServerIDs: []string{"s1"}, Revisions: map[string]int64{"s1": 5}}
	b := ReloadScope{Names: []string{"A"}, AllStale: []string{"s3"}, AppIDs: []string{"x"},
		Revisions: map[string]int64{"s1": 3, "s2": 1}, SettleBy: time.Unix(100, 0)}
	m := a.Merge(b)
	if !slices.Equal(m.Names, []string{"A", "B"}) || !slices.Equal(m.AppIDs, []string{"x"}) ||
		m.Revisions["s1"] != 5 || m.Revisions["s2"] != 1 || !m.SettleBy.Equal(time.Unix(100, 0)) {
		t.Fatalf("merged = %+v", m)
	}
	if !slices.Equal(m.Servers(), []string{"s1", "s3"}) {
		t.Fatalf("servers = %v", m.Servers())
	}
	// The later SettleBy wins either way round.
	if got := b.Merge(a).SettleBy; !got.Equal(time.Unix(100, 0)) {
		t.Fatalf("settle by = %v", got)
	}
}

func TestReloadScopeWants(t *testing.T) {
	s := ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{"s1", "s2"}, AllStale: []string{"s3"}, AppIDs: []string{"app-x"}}
	cases := []struct {
		server, app string
		stale       []string
		want        bool
	}{
		{"s1", "a", []string{"SMTP_PASS"}, true},
		{"s1", "a", []string{"OTHER"}, false},
		{"s1", "a", nil, false},
		{"s9", "a", []string{"SMTP_PASS"}, false},
		{"s3", "a", []string{"OTHER"}, true},
		{"s9", "app-x", []string{"OTHER"}, true},
		{"s3", "a", nil, false},
	}
	for _, tc := range cases {
		if got := s.Wants(tc.server, tc.app, tc.stale); got != tc.want {
			t.Errorf("Wants(%s, %s, %v) = %v", tc.server, tc.app, tc.stale, got)
		}
	}
	if !RolloutQueued.Active() || !RolloutRunning.Active() || RolloutDone.Active() || RolloutFailed.Active() || RolloutStopped.Active() {
		t.Fatal("Active")
	}
}
