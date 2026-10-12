package alert

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/alert"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	appA       = dbtest.ID(21)
)

// t0 is a fixed, second-aligned instant.
var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// ids mints predictable UUIDs (dbtest.ID(1000+n)) so ordering is stable.
func ids() func() string {
	var n atomic.Int64
	return func() string { return dbtest.ID(int(1000 + n.Add(1))) }
}

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	dbtest.SeedApp(t, d, appA, srv1, "nextcloud")
	r, err := New(d.Tx, d.Driver, dbtest.Log, ids())
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func count(t *testing.T, d *db.DB, table, where string, args ...any) string {
	t.Helper()
	return dbtest.QueryString(t, d, "SELECT CAST(COUNT(*) AS TEXT) FROM "+table+" WHERE "+where, args...)
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log, ids()); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestChannelLifecycle(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	ch, err := r.CreateChannel(ctx, alert.Channel{
		OrgID: org1, Name: "Ops", Service: "telegram", Fields: map[string]string{"ui_service": "telegram"},
		IsDefault: true, Enabled: true, URLEncrypted: "sealed-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" || ch.CreatedAt.IsZero() {
		t.Fatalf("created = %+v", ch)
	}
	got, err := r.GetChannel(ctx, org1, ch.ID)
	if err != nil || got.Name != "Ops" || got.URLEncrypted != "sealed-1" || got.Fields["ui_service"] != "telegram" ||
		!got.IsDefault || !got.Enabled || got.LastSentAt != nil {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if _, err := r.GetChannel(ctx, org2, ch.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("other org's channel: %v", err)
	}

	got.Name, got.IsDefault, got.URLEncrypted = "Pager", false, "sealed-2"
	if err := r.UpdateChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	foreign := got
	foreign.OrgID = org2
	if err := r.UpdateChannel(ctx, foreign); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("update across orgs: %v", err)
	}

	if err := r.RecordDelivery(ctx, ch.ID, t0, "boom"); err != nil {
		t.Fatal(err)
	}
	after, _ := r.GetChannel(ctx, org1, ch.ID)
	if after.LastError != "boom" || after.LastSentAt != nil || after.Name != "Pager" || after.URLEncrypted != "sealed-2" {
		t.Fatalf("after failure = %+v", after)
	}
	if err := r.RecordDelivery(ctx, ch.ID, t0.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	after, _ = r.GetChannel(ctx, org1, ch.ID)
	if after.LastError != "" || after.LastSentAt == nil || !after.LastSentAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("after success = %+v", after)
	}

	list, err := r.ListChannels(ctx, org1)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := r.DeleteChannel(ctx, org2, ch.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("delete across orgs: %v", err)
	}
	if err := r.DeleteChannel(ctx, org1, ch.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListChannels(ctx, org1); len(list) != 0 {
		t.Fatalf("still listed: %+v", list)
	}
}

func TestRuleLifecycleKeepsChannelEncoding(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	defaults, err := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 90, DurationMinutes: 10, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	silent, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetApp, Metric: alert.MetricDisk,
		Threshold: 50, DurationMinutes: 5, ChannelIDs: []string{}, Enabled: true})
	routed, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetServer, Metric: alert.MetricCPU,
		Threshold: 95, DurationMinutes: 2, ChannelIDs: []string{"c1", "c2"}, Enabled: false})

	rules, err := r.ListRules(ctx, org1)
	if err != nil || len(rules) != 3 {
		t.Fatalf("list = %+v, %v", rules, err)
	}
	// Server rules first, then metric, then threshold.
	if rules[0].ID != defaults.ID || rules[1].ID != routed.ID || rules[2].ID != silent.ID {
		t.Fatalf("order = %s %s %s", rules[0].Metric, rules[1].Metric, rules[2].Metric)
	}
	if rules[0].ChannelIDs != nil || rules[2].ChannelIDs == nil || len(rules[2].ChannelIDs) != 0 ||
		len(rules[1].ChannelIDs) != 2 || rules[1].Enabled {
		t.Fatalf("channel encoding lost: %+v", rules)
	}

	defaults.Threshold, defaults.ChannelIDs = 80, []string{"c3"}
	if err := r.UpdateRule(ctx, defaults); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetRule(ctx, org1, defaults.ID)
	if err != nil || got.Threshold != 80 || len(got.ChannelIDs) != 1 || got.TargetKind != alert.TargetServer {
		t.Fatalf("updated = %+v, %v", got, err)
	}
	if _, err := r.GetRule(ctx, org2, defaults.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("other org's rule: %v", err)
	}
	if err := r.DeleteRule(ctx, org2, defaults.ID); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("delete across orgs: %v", err)
	}
	if err := r.DeleteRule(ctx, org1, defaults.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOverridesCascadeWithTheirRule(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	rule, _ := r.CreateRule(ctx, alert.Rule{OrgID: org1, TargetKind: alert.TargetApp, Metric: alert.MetricMemory,
		Threshold: 90, DurationMinutes: 10, Enabled: true})
	th, dur := 75.0, 3
	o := alert.Override{RuleID: rule.ID, ServerID: srv1, AppID: appA, Threshold: &th, DurationMinutes: &dur, ChannelIDs: []string{}}
	if err := r.SaveOverride(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.Disabled, o.Threshold = true, nil
	if err := r.SaveOverride(ctx, o); err != nil { // upsert replaces
		t.Fatal(err)
	}
	got, err := r.TargetOverrides(ctx, alert.Target{ServerID: srv1, AppID: appA})
	if err != nil || len(got) != 1 || !got[0].Disabled || got[0].Threshold != nil ||
		got[0].DurationMinutes == nil || *got[0].DurationMinutes != 3 || got[0].ChannelIDs == nil {
		t.Fatalf("target overrides = %+v, %v", got, err)
	}
	if all, _ := r.ListOverrides(ctx, org1); len(all) != 1 {
		t.Fatalf("org overrides = %+v", all)
	}
	if all, _ := r.ListOverrides(ctx, org2); len(all) != 0 {
		t.Fatalf("other org sees overrides: %+v", all)
	}
	if err := r.DeleteRule(ctx, org1, rule.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, d, "alert_overrides", "rule_id = ?", rule.ID); n != "0" {
		t.Fatalf("overrides left after rule delete: %s", n)
	}
	if err := r.DeleteOverride(ctx, o.Key()); err != nil {
		t.Fatalf("deleting a missing override: %v", err)
	}
}

func TestSeedDefaultsOncePerOrg(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	n, err := r.SeedDefaults(ctx)
	if err != nil || n != 2 {
		t.Fatalf("seeded %d orgs, %v", n, err)
	}
	rules, _ := r.ListRules(ctx, org1)
	if len(rules) != len(alert.DefaultRules()) {
		t.Fatalf("org1 rules = %d", len(rules))
	}
	// Deleting a default is permanent: a second seed adds nothing.
	if err := r.DeleteRule(ctx, org1, rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, err := r.SeedDefaults(ctx); err != nil || n != 0 {
		t.Fatalf("second seed: %d, %v", n, err)
	}
	if got := count(t, d, "alert_rules", "organization_id = ?", org1); got != fmt.Sprint(len(alert.DefaultRules())-1) {
		t.Fatalf("org1 rules after reseed = %s", got)
	}
}

func TestServerOrgID(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if org, err := r.ServerOrgID(ctx, srv2); err != nil || org != org2 {
		t.Fatalf("srv2 org = %q, %v", org, err)
	}
	if _, err := r.ServerOrgID(ctx, dbtest.ID(99)); !errors.Is(err, alert.ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}
}
