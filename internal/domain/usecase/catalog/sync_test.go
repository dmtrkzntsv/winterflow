package catalog_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/catalog"
	"winterflow/internal/infra/db/dbtest"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

var (
	g1  = time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)
	g2  = g1.Add(24 * time.Hour)
	g3  = g2.Add(24 * time.Hour)
	now = g3.Add(time.Hour)
)

// app is a valid feed entry whose compose file runs image.
func app(slug, name, image string) feed.Entry {
	files := map[string]string{"compose.yml": "services:\n  app:\n    image: " + image + "\n    environment:\n      GREETING: hello\n" +
		"x-winterflow:\n  expose:\n    - { container: app, port: 80 }\n  variables:\n    GREETING: {}\n"}
	return feed.Entry{
		Slug: slug, Name: name, Summary: name + " in a box", Keywords: []string{slug},
		LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png", Files: files, Digest: feed.Digest(files),
	}
}

// fetcher serves the feed published last, as a signed HTTP feed would:
// a 304 when asked with its ETag.
type fetcher struct {
	mu        sync.Mutex
	feed, sig []byte
	etag      string
	err       error
	asked     []string // the ETag of every fetch
}

func (f *fetcher) FetchCatalog(_ context.Context, etag string) (model.CatalogFetch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, etag)
	switch {
	case f.err != nil:
		return model.CatalogFetch{}, f.err
	case etag != "" && etag == f.etag:
		return model.CatalogFetch{NotModified: true, ETag: etag}, nil
	}
	return model.CatalogFetch{Feed: f.feed, Sig: f.sig, ETag: f.etag}, nil
}

// publish signs a feed of apps generated at with key.
func (f *fetcher) publish(t *testing.T, key ed25519.PrivateKey, at time.Time, apps ...feed.Entry) {
	t.Helper()
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, GeneratedAt: at, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feed, f.sig, f.etag = raw, feed.Sign(key, raw), `"`+at.Format(time.RFC3339)+`"`
}

func (f *fetcher) etags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.asked...)
}

type env struct {
	repo *templaterepo.Repo
	key  ed25519.PrivateKey
	feed *fetcher
	sync *catalog.Syncer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{repo: repo, key: key, feed: &fetcher{}}
	e.sync = e.syncer(repo, pub)
	return e
}

// syncer is another process's Syncer (its own ETag) over repo.
func (e *env) syncer(repo port.CatalogRepository, pub ed25519.PublicKey) *catalog.Syncer {
	return catalog.New(catalog.Deps{Repo: repo, Fetcher: e.feed, Key: pub, Log: dbtest.Log, Now: func() time.Time { return now }})
}

func (e *env) run(t *testing.T) catalog.Result {
	t.Helper()
	res, err := e.sync.Sync(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// states are the catalog templates by slug.
func (e *env) states(t *testing.T) map[string]model.CatalogState {
	t.Helper()
	list, err := e.repo.ListCatalog(dbtest.Ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]model.CatalogState{}
	for _, st := range list {
		out[st.Slug] = st
	}
	return out
}

func TestSyncInsertsThenReadsNotModified(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Actual Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	if res := e.run(t); res.Inserted != 2 || res.NotModified {
		t.Fatalf("first sync = %+v", res)
	}
	st := e.states(t)
	if len(st) != 2 || st["budget"].Name != "Actual Budget" || st["budget"].LatestRevision != 1 ||
		st["budget"].Meta.Summary != "Actual Budget in a box" || st["notes"].FeedAt == nil || !st["notes"].FeedAt.Equal(g1) {
		t.Fatalf("states = %+v", st)
	}
	if res := e.run(t); !res.NotModified {
		t.Fatalf("second sync = %+v", res)
	}
	if got := e.feed.etags(); len(got) != 2 || got[0] != "" || got[1] != `"2026-10-14T20:00:00Z"` {
		t.Fatalf("asked with %q", got)
	}
}

// A feed whose signature does not verify is refused as a whole: nothing
// is written and the next sync fetches it again.
func TestSyncRefusesABadSignature(t *testing.T) {
	e := newEnv(t)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.feed.publish(t, other, g1, app("budget", "Budget", "actual:1"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.feed.mu.Lock()
	e.feed.feed = append(e.feed.feed[:len(e.feed.feed)-1:len(e.feed.feed)-1], ' ', '}')
	e.feed.mu.Unlock()
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("a changed feed: %v", err)
	}
	if st := e.states(t); len(st) != 0 {
		t.Fatalf("a refused feed wrote %+v", st)
	}
	if got := e.feed.etags(); got[1] != "" {
		t.Fatalf("a refused feed's ETag was kept: %q", got)
	}
}

// An unknown format or a feed without apps is refused as a whole: no
// template is withdrawn.
func TestSyncRefusesAnUnusableFeed(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	for name, raw := range map[string]string{
		"format 2": `{"format":2,"generated_at":"2026-10-15T20:00:00Z","apps":[{"slug":"x"}]}`,
		"no apps":  `{"format":1,"generated_at":"2026-10-15T20:00:00Z","apps":[]}`,
	} {
		e.feed.mu.Lock()
		e.feed.feed, e.feed.sig, e.feed.etag = []byte(raw), feed.Sign(e.key, []byte(raw)), `"`+name+`"`
		e.feed.mu.Unlock()
		if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, feed.ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if st := e.states(t)["budget"]; st.WithdrawnAt != nil {
		t.Fatalf("withdrawn by a refused feed: %+v", st)
	}
}

// An invalid entry is skipped: a new one is not inserted, an existing one
// keeps its last good revision and is not withdrawn.
func TestSyncSkipsAnInvalidEntry(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	broken := app("budget", "Budget", "actual:2")
	broken.Digest = "sha256:stale"
	bad := app("Bad Slug", "Bad", "x:1")
	e.feed.publish(t, e.key, g2, broken, bad, app("notes", "Notes", "notes:1"))
	if res := e.run(t); res.Invalid != 2 || res.Inserted != 1 || res.Withdrawn != 0 {
		t.Fatalf("sync = %+v", res)
	}
	st := e.states(t)
	if len(st) != 2 || st["budget"].LatestRevision != 1 || st["budget"].WithdrawnAt != nil || st["budget"].Digest != app("budget", "", "actual:1").Digest {
		t.Fatalf("states = %+v", st)
	}
}

// Only a change of files makes a revision; a name or metadata change is
// made in place (no "upgrade available" for a fixed description).
func TestSyncRevisesOnlyWhenFilesChange(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	e.run(t)
	fixed := app("budget", "Actual Budget", "actual:1")
	fixed.Description = "Envelope budgeting."
	e.feed.publish(t, e.key, g2, fixed)
	if res := e.run(t); res.Updated != 1 || res.Revised != 0 {
		t.Fatalf("metadata sync = %+v", res)
	}
	if st := e.states(t)["budget"]; st.LatestRevision != 1 || st.Name != "Actual Budget" || st.Meta.Description != "Envelope budgeting." {
		t.Fatalf("after metadata = %+v", st)
	}
	e.feed.publish(t, e.key, g3, app("budget", "Actual Budget", "actual:2"))
	if res := e.run(t); res.Revised != 1 {
		t.Fatalf("files sync = %+v", res)
	}
	if st := e.states(t)["budget"]; st.LatestRevision != 2 || st.Digest != app("budget", "", "actual:2").Digest {
		t.Fatalf("after files = %+v", st)
	}
}

// A template the feed drops is withdrawn; when a later feed lists it
// again it comes back, on the same revision. A renamed slug is a new
// template and the old one is withdrawn.
func TestSyncWithdrawsAndRestores(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	e.run(t)
	e.feed.publish(t, e.key, g2, app("budget", "Budget", "actual:1"), app("notes-app", "Notes", "notes:1"))
	if res := e.run(t); res.Withdrawn != 1 || res.Inserted != 1 || res.Unchanged != 1 {
		t.Fatalf("drop = %+v", res)
	}
	st := e.states(t)
	if st["notes"].WithdrawnAt == nil || !st["notes"].WithdrawnAt.Equal(now) || st["notes-app"].WithdrawnAt != nil || st["notes-app"].ID == st["notes"].ID {
		t.Fatalf("after the rename = %+v", st)
	}
	e.feed.publish(t, e.key, g3, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"), app("notes-app", "Notes", "notes:1"))
	if res := e.run(t); res.Updated != 1 || res.Withdrawn != 0 {
		t.Fatalf("restore = %+v", res)
	}
	if st := e.states(t)["notes"]; st.WithdrawnAt != nil || st.LatestRevision != 1 {
		t.Fatalf("restored = %+v", st)
	}
}

// A feed older than one already applied is refused, also by a process
// that never saw the newer one (a restart, another replica): a stale
// mirror never rolls templates back. A feed dated in the future is
// refused too: stamping it would refuse every real feed after it.
func TestSyncRefusesAnOlderOrFutureFeed(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g2, app("budget", "Budget", "actual:2"))
	e.run(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrStale) {
		t.Fatalf("an older feed: %v", err)
	}
	restarted := e.syncer(e.repo, e.key.Public().(ed25519.PublicKey))
	if _, err := restarted.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrStale) {
		t.Fatalf("an older feed after a restart: %v", err)
	}
	if st := e.states(t); len(st) != 1 || st["budget"].Digest != app("budget", "", "actual:2").Digest {
		t.Fatalf("states = %+v", st)
	}
	e.feed.publish(t, e.key, now.Add(catalog.MaxClockSkew+time.Minute), app("budget", "Budget", "actual:3"))
	if _, err := e.sync.Sync(dbtest.Ctx(t)); !errors.Is(err, catalog.ErrFuture) {
		t.Fatalf("a future feed: %v", err)
	}
	if st := e.states(t)["budget"]; !st.FeedAt.Equal(g2) {
		t.Fatalf("a refused feed was stamped: %v", st.FeedAt)
	}
}

// failingRepo fails ApplyCatalog while fail is set.
type failingRepo struct {
	port.CatalogRepository
	fail atomic.Bool
}

func (r *failingRepo) ApplyCatalog(ctx context.Context, base *model.CatalogState, e model.CatalogEntry) (model.CatalogOutcome, error) {
	if r.fail.Load() {
		return "", errors.New("database is locked")
	}
	return r.CatalogRepository.ApplyCatalog(ctx, base, e)
}

// A sync that failed midway keeps no ETag: the next one fetches the whole
// feed again instead of a 304 hiding what was never applied.
func TestSyncRetriesAFailedApply(t *testing.T) {
	e := newEnv(t)
	repo := &failingRepo{CatalogRepository: e.repo}
	repo.fail.Store(true)
	s := e.syncer(repo, e.key.Public().(ed25519.PublicKey))
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	if _, err := s.Sync(dbtest.Ctx(t)); err == nil {
		t.Fatal("a failed apply reported success")
	}
	repo.fail.Store(false)
	res, err := s.Sync(dbtest.Ctx(t))
	if err != nil || res.NotModified || res.Inserted != 1 {
		t.Fatalf("retry = %+v, %v", res, err)
	}
	if got := e.feed.etags(); len(got) != 2 || got[1] != "" {
		t.Fatalf("asked with %q", got)
	}
}

// Replicas syncing one feed at once: each template is written once, and
// every replica ends without an error.
func TestSyncRace(t *testing.T) {
	e := newEnv(t)
	pub := e.key.Public().(ed25519.PublicKey)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	var wg sync.WaitGroup
	results := make([]catalog.Result, 4)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.syncer(e.repo, pub).Sync(dbtest.Ctx(t))
			if err != nil {
				t.Error(err)
			}
			results[i] = res
		}()
	}
	wg.Wait()
	inserted := 0
	for _, r := range results {
		inserted += r.Inserted
	}
	if inserted != 2 {
		t.Fatalf("results = %+v", results)
	}
	if st := e.states(t); len(st) != 2 || st["budget"].LatestRevision != 1 {
		t.Fatalf("states = %+v", st)
	}
}

// With the catalog switched off every catalog template is withdrawn once;
// switched on again, the next sync restores them.
func TestWithdrawAll(t *testing.T) {
	e := newEnv(t)
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), app("notes", "Notes", "notes:1"))
	e.run(t)
	ctx := dbtest.Ctx(t)
	if n, err := catalog.WithdrawAll(ctx, e.repo, now); err != nil || n != 2 {
		t.Fatalf("withdraw all = %d, %v", n, err)
	}
	if n, err := catalog.WithdrawAll(ctx, e.repo, now); err != nil || n != 0 {
		t.Fatalf("again = %d, %v", n, err)
	}
	restarted := e.syncer(e.repo, e.key.Public().(ed25519.PublicKey))
	if res, err := restarted.Sync(ctx); err != nil || res.Updated != 2 {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	for slug, st := range e.states(t) {
		if st.WithdrawnAt != nil {
			t.Fatalf("%s still withdrawn", slug)
		}
	}
}

// Run syncs after First, then every Every, and a failure is not fatal.
func TestRun(t *testing.T) {
	e := newEnv(t)
	e.feed.err = errors.New("connection refused")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.sync.Run(ctx, catalog.Schedule{First: time.Millisecond, Every: 5 * time.Millisecond, Timeout: time.Second})
		close(done)
	}()
	deadline := time.After(10 * time.Second)
	for len(e.feed.etags()) < 3 {
		select {
		case <-deadline:
			t.Fatal("Run did not keep syncing after a failure")
		case <-time.After(time.Millisecond):
		}
	}
	e.feed.mu.Lock()
	e.feed.err = nil
	e.feed.mu.Unlock()
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"))
	for len(e.states(t)) == 0 {
		select {
		case <-deadline:
			t.Fatal("Run never applied the feed")
		case <-time.After(time.Millisecond):
		}
	}
	// The next tick reads a 304.
	for n := len(e.feed.etags()); len(e.feed.etags()) < n+2; {
		select {
		case <-deadline:
			t.Fatal("Run stopped after applying the feed")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
}

// A zero Schedule takes the defaults (the first sync a minute away): a
// cancelled Run returns without syncing.
func TestRunDefaults(t *testing.T) {
	e := newEnv(t)
	s := catalog.New(catalog.Deps{Repo: e.repo, Fetcher: e.feed, Log: dbtest.Log})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx, catalog.Schedule{})
	if got := e.feed.etags(); len(got) != 0 {
		t.Fatalf("synced %d times", len(got))
	}
}

// A Syncer built without a logger logs nothing and does not panic, also
// on the paths that log (an invalid entry, a failed sync).
func TestNewWithoutALogger(t *testing.T) {
	e := newEnv(t)
	s := catalog.New(catalog.Deps{Repo: e.repo, Fetcher: e.feed, Key: e.key.Public().(ed25519.PublicKey), Now: func() time.Time { return now }})
	bad := app("broken", "Broken", "x:1")
	bad.Digest = "sha256:wrong"
	e.feed.publish(t, e.key, g1, app("budget", "Budget", "actual:1"), bad)
	res, err := s.Sync(dbtest.Ctx(t))
	if err != nil || res.Invalid != 1 || res.Inserted != 1 {
		t.Fatalf("sync = %+v, %v", res, err)
	}
}
