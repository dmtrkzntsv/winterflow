package layout

import (
	"reflect"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/store"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestKeysAndIDs(t *testing.T) {
	ts := at("2026-10-11T12:30:45+02:00")
	id := PointID(ts, command.PointManual)
	if id != "20261011T103045Z-manual" {
		t.Fatalf("id = %s", id)
	}
	got, kind, ok := ParsePointID(id)
	if !ok || !got.Equal(ts) || kind != command.PointManual {
		t.Fatalf("parse = %v %v %v", got, kind, ok)
	}
	if _, _, ok := ParsePointID("20261341T103045Z-manual"); ok {
		t.Fatal("month 13 parsed")
	}
	if _, _, ok := ParsePointID("x"); ok {
		t.Fatal("garbage parsed")
	}
	if k := DataKey("app-1", "db", id, recipe.TypeSQLite); k != "app-1/db/20261011T103045Z-manual.db.zst" {
		t.Fatalf("sqlite key = %s", k)
	}
	if k := DataKey("app-1", "media", id, recipe.TypeFiles); k != "app-1/media/20261011T103045Z-manual.tar.zst" {
		t.Fatalf("files key = %s", k)
	}
	if k := ManifestKey("app-1", "db", id); k != "app-1/db/20261011T103045Z-manual.json" {
		t.Fatalf("manifest key = %s", k)
	}
}

func TestPoints(t *testing.T) {
	folder := Folder("app-1", "db")
	objs := []store.Object{
		{Key: folder + "20261010T000000Z-scheduled.db.zst", Size: 10},
		{Key: folder + "20261010T000000Z-scheduled.json", Size: 1},
		{Key: folder + "20261011T000000Z-manual.db.zst", Size: 20}, // no manifest yet
		{Key: folder + "20261009T000000Z-pre-restore.json"},        // data gone
		{Key: folder + "notes.txt"},
		{Key: folder + "sub/20261010T000000Z-scheduled.db.zst"},
		{Key: "app-1/other/20261010T000000Z-scheduled.db.zst"},
		{Key: folder + "20261010T000000Z-weekly.db.zst"},
	}
	got := Points(objs, folder)
	want := []Point{
		{ID: "20261011T000000Z-manual", Time: at("2026-10-11T00:00:00Z"), Kind: command.PointManual, DataKey: folder + "20261011T000000Z-manual.db.zst", Size: 20},
		{ID: "20261010T000000Z-scheduled", Time: at("2026-10-10T00:00:00Z"), Kind: command.PointScheduled,
			DataKey: folder + "20261010T000000Z-scheduled.db.zst", ManifestKey: folder + "20261010T000000Z-scheduled.json", Size: 10},
		{ID: "20261009T000000Z-pre-restore", Time: at("2026-10-09T00:00:00Z"), Kind: command.PointPreRestore, ManifestKey: folder + "20261009T000000Z-pre-restore.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("points =\n%+v\nwant\n%+v", got, want)
	}
	if c := Complete(got); len(c) != 1 || c[0].ID != "20261010T000000Z-scheduled" {
		t.Fatalf("complete = %+v", c)
	}
	if n, ok := Newest(got, command.PointManual, command.PointScheduled); !ok || n.Kind != command.PointScheduled {
		t.Fatalf("newest = %+v %v", n, ok)
	}
	if _, ok := Newest(got, command.PointPreRestore); ok {
		t.Fatal("an incomplete point counted as newest")
	}
	if k := got[2].Keys(); len(k) != 1 || k[0] != folder+"20261009T000000Z-pre-restore.json" {
		t.Fatalf("keys = %v", k)
	}
	info := got[1].Info(180)
	if info.ID != got[1].ID || info.Size != 10 || info.Time != at("2026-10-10T00:00:00Z").Unix() ||
		info.Expires != at("2027-04-08T00:00:00Z").Unix() {
		t.Fatalf("info = %+v", info)
	}
	if got[1].Info(0).Expires != 0 {
		t.Fatal("no ceiling expires")
	}
}

// point is a complete point of kind made at ts.
func point(ts string, kind command.PointKind) Point {
	t := at(ts)
	id := PointID(t, kind)
	return Point{ID: id, Time: t, Kind: kind, DataKey: id + ".db.zst", ManifestKey: id + ".json"}
}

func ids(ps []Point) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestPruneTiers(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	// Hourly copies over the last 3 days, then one a day for 3 more days,
	// then one per month for a few months.
	var points []Point
	for h := 0; h < 72; h++ {
		points = append(points, point(now.Add(-time.Duration(h)*time.Hour-30*time.Minute).Format(time.RFC3339), command.PointScheduled))
	}
	for _, ts := range []string{"2026-10-07T23:00:00Z", "2026-10-07T10:00:00Z", "2026-10-06T10:00:00Z", "2026-09-15T10:00:00Z",
		"2026-09-01T10:00:00Z", "2026-08-20T10:00:00Z", "2026-03-01T10:00:00Z"} {
		points = append(points, point(ts, command.PointScheduled))
	}
	policy := Policy{Keep: recipe.Retention{Recent: 24 * time.Hour, Daily: 5, Monthly: 3}}
	del := map[string]bool{}
	for _, p := range Prune(points, policy, now) {
		del[p.ID] = true
	}
	kept := []string{}
	for _, p := range points {
		if !del[p.ID] {
			kept = append(kept, p.ID)
		}
	}
	// Recent: 24 copies (11:30 back to 12:30 the day before). Daily (5 days,
	// today = day 0): the newest of each day, of which 10-11's and 10-10's
	// (23:30) are recent already, then 10-09 23:30, 10-08 23:30 and 10-07
	// 23:00. Monthly (3 months): October's newest is kept already, then
	// September 15th and August 20th. March is past both tiers.
	want := []string{}
	for h := 0; h < 24; h++ {
		want = append(want, PointID(now.Add(-time.Duration(h)*time.Hour-30*time.Minute), command.PointScheduled))
	}
	want = append(want, "20261009T233000Z-scheduled", "20261008T233000Z-scheduled",
		"20261007T230000Z-scheduled", "20260915T100000Z-scheduled", "20260820T100000Z-scheduled")
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept =\n%v\nwant\n%v", kept, want)
	}
}

func TestPruneKindsAndCeiling(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	incompleteOld := point("2026-10-10T10:00:00Z", command.PointManual)
	incompleteOld.ManifestKey = ""
	incompleteNew := point("2026-10-11T11:00:00Z", command.PointScheduled)
	incompleteNew.ManifestKey = ""
	points := []Point{
		incompleteNew,
		incompleteOld,
		point("2026-09-01T10:00:00Z", command.PointManual),     // manual: only the ceiling
		point("2026-09-01T09:00:00Z", command.PointPreRestore), // older than 30 days
		point("2026-09-20T09:00:00Z", command.PointPreRestore), // younger
		point("2026-01-01T10:00:00Z", command.PointScheduled),  // newest scheduled: kept even past every tier
		point("2025-12-31T10:00:00Z", command.PointScheduled),  // past every tier
		point("2025-01-01T10:00:00Z", command.PointManual),     // past the ceiling
	}
	policy := Policy{Keep: recipe.Retention{Recent: time.Hour, Daily: 1, Monthly: 1}, CeilingDays: 365}
	got := ids(Prune(points, policy, now))
	want := []string{"20261010T100000Z-manual", "20260901T090000Z-pre-restore", "20251231T100000Z-scheduled", "20250101T100000Z-manual"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pruned = %v, want %v", got, want)
	}
	// The ceiling overrides keeping the newest scheduled copy.
	policy.CeilingDays = 200
	got = ids(Prune(points, policy, now))
	if got[len(got)-3] != "20260101T100000Z-scheduled" {
		t.Fatalf("pruned under a 200 day ceiling = %v", got)
	}
	// No ceiling: the manual copy from 2025 stays.
	policy.CeilingDays = 0
	for _, id := range ids(Prune(points, policy, now)) {
		if id == "20250101T100000Z-manual" {
			t.Fatal("a manual copy was pruned without a ceiling")
		}
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	old := []Point{point("2026-07-01T00:00:00Z", command.PointScheduled), point("2026-10-10T00:00:00Z", command.PointManual)}
	if !NeedsRefresh(old, 180, now) {
		t.Fatal("a scheduled copy older than half the ceiling needs a refresh")
	}
	if NeedsRefresh(old, 0, now) {
		t.Fatal("no ceiling, no refresh")
	}
	if NeedsRefresh(old, 400, now) {
		t.Fatal("younger than half the ceiling")
	}
	if !NeedsRefresh(old[1:], 180, now) {
		t.Fatal("only a manual copy under a ceiling: a scheduled copy is due")
	}
	if !NeedsRefresh(nil, 180, now) {
		t.Fatal("no copies under a ceiling: a scheduled copy is due")
	}
	if NeedsRefresh(nil, 0, now) || NeedsRefresh(old[1:], 0, now) {
		t.Fatal("no ceiling, no refresh")
	}
	// A ceiling beyond the cap behaves as the cap (no duration overflow).
	if NeedsRefresh(old, 213504, now) || NeedsRefresh(old, 365000, now) {
		t.Fatal("a huge ceiling refreshed a young copy")
	}
	if e := Expires(old[0], 365000); !e.Equal(old[0].Time.Add(36500 * 24 * time.Hour)) {
		t.Fatalf("huge ceiling expires %v", e)
	}
	if got := Prune(old, Policy{CeilingDays: 213504}, now); len(got) != 0 {
		t.Fatalf("a huge ceiling pruned %v", ids(got))
	}
	if e := Expires(old[0], 0); !e.IsZero() {
		t.Fatal("no ceiling never expires")
	}
}
