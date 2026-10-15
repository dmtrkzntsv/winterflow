package template

import (
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var (
	feed1 = time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	feed2 = feed1.Add(24 * time.Hour)
)

// catalogEntry is a feed entry for slug with digest d.
func catalogEntry(slug, name, d string, at time.Time) model.CatalogEntry {
	return model.CatalogEntry{
		Slug: slug, Name: name, Digest: d, FeedAt: at, Body: []byte(`{"files":{"compose.yml":"` + d + `"}}`),
		Meta: model.CatalogMeta{Summary: "About " + name, Keywords: []string{"k"}, LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png"},
	}
}

// state is the sync state of slug (fails the test when there is none).
func state(t *testing.T, r *Repo, slug string) *model.CatalogState {
	t.Helper()
	list, err := r.ListCatalog(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := range list {
		if list[i].Slug == slug {
			return &list[i]
		}
	}
	t.Fatalf("no catalog template %q in %+v", slug, list)
	return nil
}

func apply(t *testing.T, r *Repo, base *model.CatalogState, e model.CatalogEntry) model.CatalogOutcome {
	t.Helper()
	out, err := r.ApplyCatalog(dbtest.Ctx(t), base, e)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A new slug inserts revision 1; a digest change adds a revision; a name
// or metadata change is made in place; the metadata reads back on the
// template every org sees.
func TestApplyCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if got := apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1)); got != model.CatalogInserted {
		t.Fatalf("insert = %s", got)
	}
	st := state(t, r, "budget")
	if st.Name != "Budget" || st.LatestRevision != 1 || st.Digest != "sha256:a" || st.WithdrawnAt != nil ||
		st.FeedAt == nil || !st.FeedAt.Equal(feed1) || st.Meta.Summary != "About Budget" || st.Meta.Tags == nil {
		t.Fatalf("state = %+v", st)
	}
	tpl, err := r.GetTemplate(ctx, org2, st.ID)
	if err != nil || !tpl.Catalog() || tpl.CatalogMeta == nil || tpl.CatalogMeta.LogoURL != "https://winterflow.io/catalog/logos/budget.png" {
		t.Fatalf("template = %+v, %v", tpl, err)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 1 || list[0].CatalogMeta == nil || list[0].CatalogMeta.Summary != "About Budget" {
		t.Fatalf("list = %+v", list)
	}

	renamed := catalogEntry("budget", "Actual Budget", "sha256:a", feed2)
	renamed.Meta.Description = "Fixed."
	if got := apply(t, r, st, renamed); got != model.CatalogUpdated {
		t.Fatalf("rename = %s", got)
	}
	st = state(t, r, "budget")
	if st.Name != "Actual Budget" || st.LatestRevision != 1 || st.Meta.Description != "Fixed." || !st.FeedAt.Equal(feed2) {
		t.Fatalf("after the rename = %+v", st)
	}

	if got := apply(t, r, st, catalogEntry("budget", "Actual Budget", "sha256:b", feed2)); got != model.CatalogRevised {
		t.Fatalf("revise = %s", got)
	}
	st = state(t, r, "budget")
	rev, err := r.GetRevision(ctx, st.ID, 2)
	if err != nil || st.LatestRevision != 2 || st.Digest != "sha256:b" || rev.Digest != "sha256:b" || string(rev.Body) != `{"files":{"compose.yml":"sha256:b"}}` {
		t.Fatalf("after the revision = %+v, %+v, %v", st, rev, err)
	}
	if revs, _ := r.ListRevisions(ctx, st.ID); len(revs) != 2 {
		t.Fatalf("revisions = %+v", revs)
	}
}

// A withdrawn template leaves the lists of orgs without instances; the
// next feed that lists it again brings it back.
func TestWithdrawAndRestoreCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1))
	st := state(t, r, "budget")
	at := feed2.Add(time.Minute)
	if ok, err := r.WithdrawCatalog(ctx, st.ID, at, feed2); err != nil || !ok {
		t.Fatalf("withdraw = %v, %v", ok, err)
	}
	if ok, err := r.WithdrawCatalog(ctx, st.ID, at.Add(time.Hour), feed2); err != nil || ok {
		t.Fatalf("withdraw twice = %v, %v", ok, err)
	}
	st = state(t, r, "budget")
	if st.WithdrawnAt == nil || !st.WithdrawnAt.Equal(at) {
		t.Fatalf("withdrawn = %+v", st)
	}
	if list, _ := r.ListTemplates(ctx, org1); len(list) != 0 {
		t.Fatalf("a withdrawn template is listed: %+v", list)
	}
	if got := apply(t, r, st, catalogEntry("budget", "Budget", "sha256:a", feed2)); got != model.CatalogUpdated {
		t.Fatalf("restore = %s", got)
	}
	if st = state(t, r, "budget"); st.WithdrawnAt != nil || st.LatestRevision != 1 {
		t.Fatalf("restored = %+v", st)
	}
}

// A write from an older feed than the one that wrote the template, or
// from a stale base, is skipped and changes nothing: a stale mirror never
// rolls a template back, and two replicas never add two revisions.
func TestApplyCatalogSkipsStaleWrites(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed1))
	base := state(t, r, "budget")
	apply(t, r, base, catalogEntry("budget", "Budget", "sha256:b", feed2))

	for name, c := range map[string]struct {
		base *model.CatalogState
		e    model.CatalogEntry
	}{
		"the slug inserted meanwhile":    {nil, catalogEntry("budget", "Budget", "sha256:c", feed2)},
		"a stale base":                   {base, catalogEntry("budget", "Budget", "sha256:c", feed2)},
		"an older feed's files":          {state(t, r, "budget"), catalogEntry("budget", "Budget", "sha256:a", feed1)},
		"an older feed's name":           {state(t, r, "budget"), catalogEntry("budget", "Old name", "sha256:b", feed1)},
		"an older feed's slug, inserted": {nil, catalogEntry("budget", "Budget", "sha256:a", feed1)},
	} {
		if got := apply(t, r, c.base, c.e); got != model.CatalogSkipped {
			t.Errorf("%s: %s", name, got)
		}
	}
	if st := state(t, r, "budget"); st.LatestRevision != 2 || st.Digest != "sha256:b" || st.Name != "Budget" {
		t.Fatalf("after the stale writes = %+v", st)
	}
	if ok, err := r.WithdrawCatalog(ctx, base.ID, feed2, feed1); err != nil || ok {
		t.Fatalf("an older feed withdrew it: %v, %v", ok, err)
	}
}

// StampCatalog moves every catalog template's feed forward, never back;
// a template no sync wrote (inserted by CreateTemplate) gets one too.
func TestStampCatalog(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCatalog(t, r, cat1, "manual", "Manual", nil)
	apply(t, r, nil, catalogEntry("budget", "Budget", "sha256:a", feed2))
	if err := r.StampCatalog(ctx, feed1); err != nil {
		t.Fatal(err)
	}
	if st := state(t, r, "budget"); !st.FeedAt.Equal(feed2) {
		t.Fatalf("stamped back to %v", st.FeedAt)
	}
	if st := state(t, r, "manual"); st.FeedAt == nil || !st.FeedAt.Equal(feed1) || st.Meta.Keywords == nil {
		t.Fatalf("manual = %+v", st)
	}
	if err := r.StampCatalog(ctx, feed2.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st := state(t, r, "budget"); !st.FeedAt.Equal(feed2.Add(time.Hour)) {
		t.Fatalf("not stamped forward: %v", st.FeedAt)
	}
}

// Replicas applying the same feed at once: one write wins each step, the
// others are skipped, and no revision is added twice (on Postgres the
// transactions really overlap).
func TestApplyCatalogRace(t *testing.T) {
	r, _ := newRepo(t)
	race := func(base *model.CatalogState, e model.CatalogEntry) map[model.CatalogOutcome]int {
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			got = map[model.CatalogOutcome]int{}
		)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				out, err := r.ApplyCatalog(dbtest.Ctx(t), base, e)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				got[out]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		return got
	}
	if got := race(nil, catalogEntry("budget", "Budget", "sha256:a", feed1)); got[model.CatalogInserted] != 1 || got[model.CatalogSkipped] != 3 {
		t.Fatalf("inserts = %v", got)
	}
	if got := race(state(t, r, "budget"), catalogEntry("budget", "Budget", "sha256:b", feed2)); got[model.CatalogRevised] != 1 || got[model.CatalogSkipped] != 3 {
		t.Fatalf("revisions = %v", got)
	}
	if st := state(t, r, "budget"); st.LatestRevision != 2 || st.Digest != "sha256:b" {
		t.Fatalf("after the race = %+v", st)
	}
}
