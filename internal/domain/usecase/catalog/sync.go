// Package catalog is the API's catalog sync: it fetches the signed feed,
// checks it, and brings the catalog templates to it (new templates, new
// revisions when files change, metadata in place, withdrawals). One
// goroutine per API process runs it (Run), off the standalone bridge's
// goroutine; every write is conditional, so replicas syncing at once are
// harmless.
package catalog

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	feed "winterflow/internal/domain/catalog"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	"winterflow/pkg/logger"
)

// Defaults of Schedule.
const (
	DefaultFirst   = time.Minute
	DefaultEvery   = 24 * time.Hour
	DefaultTimeout = 5 * time.Minute
	// MaxClockSkew is how far in the future a feed's generated_at may be:
	// a feed stamped further ahead (a builder with a wrong clock) would
	// make every later feed read as older, so it is refused.
	MaxClockSkew = time.Hour
)

// Errors that refuse a whole feed (besides feed.ErrSignature and
// feed.ErrInvalid).
var (
	// ErrStale: the feed is older than one already applied, by this
	// process or another replica.
	ErrStale = errors.New("the catalog feed is older than the one applied")
	// ErrFuture: the feed's generated_at is past MaxClockSkew ahead.
	ErrFuture = errors.New("the catalog feed is dated in the future")
)

// anyFeed is the feedAt of a withdrawal no feed can outrank (the catalog
// switched off).
var anyFeed = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// Deps wires a Syncer. Now may be nil (time.Now); Log may be nil (logs
// nothing).
type Deps struct {
	Repo    port.CatalogRepository
	Fetcher port.CatalogFetcher
	Key     ed25519.PublicKey
	Log     *logger.Logger
	Now     func() time.Time
}

// Syncer brings the catalog templates to the feed.
type Syncer struct {
	repo    port.CatalogRepository
	fetcher port.CatalogFetcher
	key     ed25519.PublicKey
	log     *logger.Logger
	now     func() time.Time

	mu sync.Mutex // one sync at a time
	// etag is the last fully applied feed's: kept only after a sync
	// succeeded, so a failed one fetches the whole feed again instead of
	// reading a 304.
	etag string
}

// New builds a Syncer.
func New(d Deps) *Syncer {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	log := d.Log
	if log == nil {
		log = &logger.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	return &Syncer{repo: d.Repo, fetcher: d.Fetcher, key: d.Key, log: log, now: now}
}

// Result is what one sync did.
type Result struct {
	NotModified bool
	Inserted    int
	Revised     int
	Updated     int
	Unchanged   int
	// Skipped entries another replica (or a newer feed) wrote first.
	Skipped int
	// Invalid entries failed the check: logged and left alone (an existing
	// template keeps its last good revision and is not withdrawn).
	Invalid   int
	Withdrawn int
}

// Sync fetches the feed (with the last applied ETag) and applies it. The
// whole feed is refused when its signature does not verify against the
// key, its format is unknown, it lists no app, or it is older than a feed
// already applied (ErrStale) or dated in the future (ErrFuture). Each
// valid entry is applied; templates the feed no longer lists are
// withdrawn; the feed's generated_at is then stamped on every catalog
// template.
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	got, err := s.fetcher.FetchCatalog(ctx, s.etag)
	if err != nil {
		return Result{}, err
	}
	if got.NotModified {
		return Result{NotModified: true}, nil
	}
	if err := feed.Verify(s.key, got.Feed, got.Sig); err != nil {
		return Result{}, err
	}
	f, err := feed.Decode(got.Feed)
	if err != nil {
		return Result{}, err
	}
	now := s.now()
	if f.GeneratedAt.After(now.Add(MaxClockSkew)) {
		return Result{}, fmt.Errorf("%w: generated_at %s", ErrFuture, f.GeneratedAt.UTC().Format(time.RFC3339))
	}
	states, err := s.repo.ListCatalog(ctx)
	if err != nil {
		return Result{}, err
	}
	bySlug := make(map[string]*model.CatalogState, len(states))
	for i := range states {
		st := &states[i]
		bySlug[st.Slug] = st
		if st.FeedAt != nil && f.GeneratedAt.Before(*st.FeedAt) {
			return Result{}, fmt.Errorf("%w: generated %s, applied %s", ErrStale,
				f.GeneratedAt.UTC().Format(time.RFC3339), st.FeedAt.UTC().Format(time.RFC3339))
		}
	}
	var res Result
	listed := make(map[string]bool, len(f.Apps))
	for _, e := range f.Apps {
		listed[e.Slug] = true
		if err := s.apply(ctx, e, bySlug[e.Slug], f.GeneratedAt, &res); err != nil {
			return res, err
		}
	}
	for _, st := range states {
		if listed[st.Slug] || st.WithdrawnAt != nil {
			continue
		}
		ok, err := s.repo.WithdrawCatalog(ctx, st.ID, now, f.GeneratedAt)
		if err != nil {
			return res, fmt.Errorf("catalog %s: %w", st.Slug, err)
		}
		if ok {
			res.Withdrawn++
		}
	}
	if err := s.repo.StampCatalog(ctx, f.GeneratedAt); err != nil {
		return res, err
	}
	s.etag = got.ETag
	return res, nil
}

// apply checks one entry and writes it unless the template already says
// the same.
func (s *Syncer) apply(ctx context.Context, e feed.Entry, base *model.CatalogState, feedAt time.Time, res *Result) error {
	body, meta, err := e.Check()
	if err != nil {
		res.Invalid++
		s.log.Warn("catalog: skipping an invalid entry", "slug", e.Slug, "error", err)
		return nil
	}
	if base != nil && base.Digest == e.Digest && base.Name == e.Name && base.Meta.Equal(meta) && base.WithdrawnAt == nil {
		res.Unchanged++
		return nil
	}
	raw, err := template.Encode(body)
	if err != nil {
		return err
	}
	out, err := s.repo.ApplyCatalog(ctx, base, model.CatalogEntry{
		Slug: e.Slug, Name: e.Name, Meta: meta, Body: raw, Digest: e.Digest, FeedAt: feedAt,
	})
	if err != nil {
		return fmt.Errorf("catalog %s: %w", e.Slug, err)
	}
	switch out {
	case model.CatalogInserted:
		res.Inserted++
	case model.CatalogRevised:
		res.Revised++
	case model.CatalogUpdated:
		res.Updated++
	default:
		res.Skipped++
	}
	return nil
}

// Schedule is when Run syncs; zero fields take the defaults.
type Schedule struct {
	// First is the wait before the first sync (DefaultFirst).
	First time.Duration
	// Every is the wait between syncs (DefaultEvery; CATALOG_REFRESH).
	Every time.Duration
	// Timeout bounds one sync (DefaultTimeout).
	Timeout time.Duration
}

// Run syncs after sch.First, then every sch.Every, until ctx ends. A
// failed sync is logged and retried at the next tick; nothing is fatal.
func (s *Syncer) Run(ctx context.Context, sch Schedule) {
	if sch.First <= 0 {
		sch.First = DefaultFirst
	}
	if sch.Every <= 0 {
		sch.Every = DefaultEvery
	}
	if sch.Timeout <= 0 {
		sch.Timeout = DefaultTimeout
	}
	timer := time.NewTimer(sch.First)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		s.runOnce(ctx, sch.Timeout)
		timer.Reset(sch.Every)
	}
}

func (s *Syncer) runOnce(ctx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := s.Sync(ctx)
	switch {
	case err != nil:
		s.log.Error("catalog sync failed", "error", err)
	case res.NotModified:
		s.log.Debug("catalog: the feed has not changed")
	default:
		s.log.Info("catalog synced", "inserted", res.Inserted, "revised", res.Revised, "updated", res.Updated,
			"unchanged", res.Unchanged, "skipped", res.Skipped, "invalid", res.Invalid, "withdrawn", res.Withdrawn)
	}
}

// WithdrawAll withdraws every catalog template: CATALOG_URL is empty, so
// the catalog is hidden (instances keep working; setting the URL again
// restores the templates at the next sync). It returns how many it
// withdrew.
func WithdrawAll(ctx context.Context, repo port.CatalogRepository, now time.Time) (int, error) {
	states, err := repo.ListCatalog(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, st := range states {
		if st.WithdrawnAt != nil {
			continue
		}
		ok, err := repo.WithdrawCatalog(ctx, st.ID, now, anyFeed)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}
