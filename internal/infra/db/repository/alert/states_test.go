package alert

import (
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db/dbtest"
)

func event(r *Repo, org, ruleID, server, app string, kind alert.EventKind, at time.Time) alert.Event {
	return alert.Event{
		ID: r.newID(), OrgID: org, RuleID: ruleID, ServerID: server, AppID: app, ServerName: "prod-1",
		TargetKind: alert.TargetServer, Metric: alert.MetricCPU, Kind: kind, Value: 93, Threshold: 90,
		Title: "t", Message: "m", CreatedAt: at,
	}
}

func cpuRule(t *testing.T, r *Repo, org string) alert.Rule {
	t.Helper()
	rule, err := r.CreateRule(dbtest.Ctx(t), alert.Rule{OrgID: org, TargetKind: alert.TargetServer,
		Metric: alert.MetricCPU, Threshold: 90, DurationMinutes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestFireIsCompareAndSet(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}

	won, err := r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0))
	if err != nil || !won {
		t.Fatalf("first fire: won=%v err=%v", won, err)
	}
	// A second replica (or a replay) loses and writes no event.
	won, err = r.Fire(ctx, k, 95, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(time.Minute)))
	if err != nil || won {
		t.Fatalf("second fire: won=%v err=%v", won, err)
	}
	if n := count(t, d, "alert_events", "rule_id = ?", rule.ID); n != "1" {
		t.Fatalf("events = %s", n)
	}
	states, err := r.FiringStates(ctx)
	if err != nil || len(states) != 1 || states[0].Key != k || states[0].Metric != alert.MetricCPU ||
		states[0].Value != 93 || !states[0].Since.Equal(t0) {
		t.Fatalf("firing = %+v, %v", states, err)
	}
	active, _ := r.ListActive(ctx, org1)
	if len(active) != 1 || active[0].Threshold != 90 || active[0].TargetKind != alert.TargetServer {
		t.Fatalf("active = %+v", active)
	}
	if other, _ := r.ListActive(ctx, org2); len(other) != 0 {
		t.Fatalf("other org sees %+v", other)
	}
	counts, _ := r.CountFiring(ctx)
	if counts[srv1] != 1 || counts[srv2] != 0 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestResolveIsCompareAndSet(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}

	// Nothing is firing: resolving is a no-op.
	if won, err := r.Resolve(ctx, k, 10, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0)); err != nil || won {
		t.Fatalf("resolve while ok: won=%v err=%v", won, err)
	}
	if _, err := r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0)); err != nil {
		t.Fatal(err)
	}
	if won, err := r.Resolve(ctx, k, 41, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0.Add(time.Minute))); err != nil || !won {
		t.Fatalf("resolve: won=%v err=%v", won, err)
	}
	if won, _ := r.Resolve(ctx, k, 40, event(r, org1, rule.ID, srv1, "", alert.EventResolved, t0.Add(2*time.Minute))); won {
		t.Fatal("second resolve won")
	}
	if n := count(t, d, "alert_events", "rule_id = ?", rule.ID); n != "2" {
		t.Fatalf("events = %s", n)
	}
	if states, _ := r.FiringStates(ctx); len(states) != 0 {
		t.Fatalf("still firing: %+v", states)
	}
	// It can fire again after resolving.
	if won, _ := r.Fire(ctx, k, 99, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(3*time.Minute))); !won {
		t.Fatal("re-fire lost")
	}
	if err := r.ClearState(ctx, k); err != nil {
		t.Fatal(err)
	}
	if states, _ := r.FiringStates(ctx); len(states) != 0 {
		t.Fatalf("cleared state still firing: %+v", states)
	}
}

func TestListEventsFiltersAndPages(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	k := alert.Key{RuleID: rule.ID, Target: alert.Target{ServerID: srv1}}
	// Alternate fire/resolve: 6 events, one per minute, newest last.
	for i := 0; i < 6; i++ {
		at := t0.Add(time.Duration(i) * time.Minute)
		if i%2 == 0 {
			_, _ = r.Fire(ctx, k, 93, 90, event(r, org1, rule.ID, srv1, "", alert.EventFired, at))
		} else {
			_, _ = r.Resolve(ctx, k, 41, event(r, org1, rule.ID, srv1, "", alert.EventResolved, at))
		}
	}
	page, err := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 4})
	if err != nil || len(page) != 4 || !page[0].CreatedAt.Equal(t0.Add(5*time.Minute)) || page[0].Deliveries == nil {
		t.Fatalf("page 1 = %+v, %v", page, err)
	}
	last := page[len(page)-1]
	rest, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 4, BeforeAt: last.CreatedAt, BeforeID: last.ID})
	if len(rest) != 2 || !rest[1].CreatedAt.Equal(t0) {
		t.Fatalf("page 2 = %+v", rest)
	}
	fired, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Kind: alert.EventFired, Limit: 50})
	if len(fired) != 3 {
		t.Fatalf("fired = %d", len(fired))
	}
	for _, q := range []alert.EventQuery{
		{OrgID: org2, Limit: 50},
		{OrgID: org1, ServerID: srv2, Limit: 50},
		{OrgID: org1, AppID: appA, Limit: 50},
		{OrgID: org1, RuleID: dbtest.ID(77), Limit: 50},
	} {
		if got, _ := r.ListEvents(ctx, q); len(got) != 0 {
			t.Errorf("%+v matched %d", q, len(got))
		}
	}
	if got, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, ServerID: srv1, RuleID: rule.ID, Limit: 50}); len(got) != 6 {
		t.Errorf("server+rule filter = %d", len(got))
	}

	ds := []alert.Delivery{{ChannelID: "c1", ChannelName: "Ops", OK: false, Error: "boom"}}
	if err := r.SetDeliveries(ctx, page[0].ID, ds); err != nil {
		t.Fatal(err)
	}
	again, _ := r.ListEvents(ctx, alert.EventQuery{OrgID: org1, Limit: 1})
	if len(again[0].Deliveries) != 1 || again[0].Deliveries[0].Error != "boom" {
		t.Fatalf("deliveries = %+v", again[0].Deliveries)
	}
}

func TestPruneEventsByAgeAndCap(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule := cpuRule(t, r, org1)
	old := t0.Add(-EventRetention - time.Hour)
	ev := event(r, org1, rule.ID, srv1, "", alert.EventFired, old)
	if err := r.insertEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	// EventsPerOrg+3 recent events, one second apart.
	for i := 0; i < EventsPerOrg+3; i++ {
		if err := r.insertEvent(ctx, event(r, org1, rule.ID, srv1, "", alert.EventFired, t0.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	n, err := r.PruneEvents(ctx, t0.Add(time.Hour))
	if err != nil || n != 4 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if got := count(t, d, "alert_events", "organization_id = ?", org1); got != fmt.Sprint(EventsPerOrg) {
		t.Fatalf("kept %s", got)
	}
	// The oldest recent ones went; the newest stayed.
	if got := count(t, d, "alert_events", "created_at < ?", stamp(t0.Add(3*time.Second))); got != "0" {
		t.Fatalf("old recent events kept: %s", got)
	}
}

func TestDirectory(t *testing.T) {
	r, _ := newRepo(t)
	dir, err := r.Directory(dbtest.Ctx(t))
	if err != nil || len(dir.Servers) != 2 || dir.Servers[srv1].OrgID != org1 ||
		dir.Apps[appA].ServerID != srv1 || dir.Apps[appA].Name != "nextcloud" {
		t.Fatalf("directory = %+v, %v", dir, err)
	}
}
