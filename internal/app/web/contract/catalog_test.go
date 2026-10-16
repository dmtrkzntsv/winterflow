package contract_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
	catalogsync "winterflow/internal/domain/usecase/catalog"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/catalogfeed"
)

var (
	feedG1 = time.Date(2026, 1, 10, 20, 0, 0, 0, time.UTC) // in the past: a feed from the future is refused
	feedG2 = feedG1.Add(24 * time.Hour)
	feedG3 = feedG2.Add(24 * time.Hour)
)

// feedServer serves a signed catalog feed on loopback the way
// winterflow.io does: catalog.json with an ETag (304 on If-None-Match)
// and catalog.json.sig.
type feedServer struct {
	*httptest.Server
	key ed25519.PrivateKey
	pub ed25519.PublicKey

	mu          sync.Mutex
	raw, sig    []byte
	etag        string
	notModified int
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &feedServer{key: key, pub: pub}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/catalog/catalog.json":
			if s.etag != "" && r.Header.Get("If-None-Match") == s.etag {
				s.notModified++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", s.etag)
			_, _ = w.Write(s.raw)
		case "/catalog/catalog.json.sig":
			if s.sig == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(s.sig)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// publish replaces the feed with apps, generated at.
func (s *feedServer) publish(t *testing.T, at time.Time, apps ...feed.Entry) {
	t.Helper()
	raw, err := feed.Encode(feed.Feed{Format: feed.FormatVersion, SourceCommit: "49bf28d", GeneratedAt: at, Apps: apps})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw, s.sig, s.etag = raw, feed.Sign(s.key, raw), `"`+at.Format(time.RFC3339)+`"`
}

// replica is one API process's sync of this feed over e's database.
func (s *feedServer) replica(t *testing.T, e *env) *catalogsync.Syncer {
	t.Helper()
	f, err := catalogfeed.New(s.URL + "/catalog/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	return catalogsync.New(catalogsync.Deps{Repo: e.deps.CatalogRepository, Fetcher: f, Key: s.pub, Log: testLog})
}

// catalogApp is a feed entry of compose (compose.yml).
func catalogApp(slug, name, compose string) feed.Entry {
	files := map[string]string{"compose.yml": compose}
	return feed.Entry{
		Slug: slug, Name: name, Summary: name + " for everyone", Keywords: []string{slug}, AlternativeTo: []string{"Other " + name},
		License: "MIT", HomepageURL: "https://" + slug + ".example.org",
		LogoURL: "https://winterflow.io/catalog/logos/" + slug + ".png", Files: files, Digest: feed.Digest(files),
	}
}

const notesCompose = "services:\n  app:\n    image: example/notes:1\n"

func syncOK(t *testing.T, s *catalogsync.Syncer, e *env) catalogsync.Result {
	t.Helper()
	res, err := s.Sync(e.dbCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// catalogBySlug is get-templates' catalog templates by slug.
func catalogBySlug(t *testing.T, c *client) map[string]tuc.TemplateSummary {
	t.Helper()
	var list tuc.TemplateList
	c.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
	out := map[string]tuc.TemplateSummary{}
	for _, s := range list.Templates {
		if s.Source == "catalog" {
			out[s.Slug] = s
		}
	}
	return out
}

// The sync fills the catalog every org reads; a 304 changes nothing; a
// new revision reads "upgrade available" on the org's instances; a
// dropped app is withdrawn and comes back when the feed lists it again.
func TestCatalogSync(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner := ie.e, ie.owner
	fs := newFeedServer(t)
	s := fs.replica(t, e)
	broken := catalogApp("broken", "Broken", notesCompose)
	broken.Digest = "sha256:stale"
	fs.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose), catalogApp("notes", "Notes", notesCompose), broken)
	if res := syncOK(t, s, e); res.Inserted != 2 || res.Invalid != 1 {
		t.Fatalf("first sync = %+v", res)
	}

	mallory, _ := e.foreignClient(t)
	for _, c := range []*client{owner, mallory} {
		got := catalogBySlug(t, c)
		if len(got) != 2 || got["shop"].Name != "Shop" || got["shop"].Catalog == nil ||
			got["shop"].Catalog.LogoURL != "https://winterflow.io/catalog/logos/shop.png" || got["notes"].Catalog.AlternativeTo[0] != "Other Notes" {
			t.Fatalf("catalog = %+v", got)
		}
	}
	shopID := catalogBySlug(t, owner)["shop"].ID
	var view tuc.TemplateView
	owner.get(t, "/api/v1/template/get-template", url.Values{"id": {shopID}}).expect(t, http.StatusOK).data(t, &view)
	if view.Source != "catalog" || view.Body.Files["compose.yml"] != shopCompose || view.Body.Summary != "Shop for everyone" ||
		len(view.Detected.Variables) != 3 || view.Catalog == nil || view.Catalog.License != "MIT" {
		t.Fatalf("view = %+v", view)
	}
	if res := syncOK(t, s, e); !res.NotModified {
		t.Fatalf("second sync = %+v", res)
	}
	fs.mu.Lock()
	notModified := fs.notModified
	fs.mu.Unlock()
	if notModified != 1 {
		t.Fatalf("304s = %d", notModified)
	}

	// An instance of the catalog's shop, then the catalog changes shop's
	// files and drops notes.
	in := ie.create("cat-1", "cata")
	in.TemplateID = shopID
	owner.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusOK)
	shop2 := catalogApp("shop", "Shop", strings.Replace(shopCompose, "example/shop:1", "example/shop:2", 1))
	fs.publish(t, feedG2, shop2)
	if res := syncOK(t, s, e); res.Revised != 1 || res.Withdrawn != 1 {
		t.Fatalf("change = %+v", res)
	}
	got := catalogBySlug(t, owner)
	if len(got) != 1 || got["shop"].LatestRevision != 2 || got["shop"].Instances != 1 || got["shop"].Behind != 1 {
		t.Fatalf("after the change = %+v", got)
	}
	fs.publish(t, feedG3, shop2, catalogApp("notes", "Notes", notesCompose))
	if res := syncOK(t, s, e); res.Updated != 1 || res.Unchanged != 1 {
		t.Fatalf("restore = %+v", res)
	}
	if got := catalogBySlug(t, owner); len(got) != 2 || got["notes"].WithdrawnAt != nil || got["notes"].LatestRevision != 1 {
		t.Fatalf("after the restore = %+v", got)
	}
}

// A feed whose signature does not verify, or whose .sig can't be fetched,
// writes nothing.
func TestCatalogSyncRefusesUnsignedFeeds(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	fs := newFeedServer(t)
	s := fs.replica(t, e)
	fs.publish(t, feedG1, catalogApp("notes", "Notes", notesCompose))
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	fs.mu.Lock()
	fs.sig = feed.Sign(other, fs.raw)
	fs.mu.Unlock()
	if _, err := s.Sync(e.dbCtx(t)); !errors.Is(err, feed.ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	fs.mu.Lock()
	fs.sig = nil
	fs.mu.Unlock()
	if _, err := s.Sync(e.dbCtx(t)); err == nil || !strings.Contains(err.Error(), "catalog.json.sig: 404") {
		t.Fatalf("no .sig: %v", err)
	}
	if got := catalogBySlug(t, owner); len(got) != 0 {
		t.Fatalf("catalog = %+v", got)
	}
}

// Replicas syncing at once (separate processes over one database; on
// Postgres their transactions overlap) write each change once; a replica
// behind a stale mirror is refused and rolls nothing back.
func TestCatalogSyncReplicas(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	fs := newFeedServer(t)
	replicas := []*catalogsync.Syncer{fs.replica(t, e), fs.replica(t, e), fs.replica(t, e)}
	race := func() catalogsync.Result {
		var (
			wg  sync.WaitGroup
			mu  sync.Mutex
			sum catalogsync.Result
		)
		for _, s := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := s.Sync(e.dbCtx(t))
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				sum.Inserted += res.Inserted
				sum.Revised += res.Revised
				mu.Unlock()
			}()
		}
		wg.Wait()
		return sum
	}
	fs.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose), catalogApp("notes", "Notes", notesCompose))
	if sum := race(); sum.Inserted != 2 {
		t.Fatalf("inserted %d times", sum.Inserted)
	}
	shop2 := catalogApp("shop", "Shop", strings.Replace(shopCompose, "example/shop:1", "example/shop:2", 1))
	fs.publish(t, feedG2, shop2, catalogApp("notes", "Notes", notesCompose))
	if sum := race(); sum.Revised != 1 {
		t.Fatalf("revised %d times", sum.Revised)
	}
	got := catalogBySlug(t, owner)
	if len(got) != 2 || got["shop"].LatestRevision != 2 || got["notes"].LatestRevision != 1 {
		t.Fatalf("catalog = %+v", got)
	}

	stale := newFeedServer(t)
	stale.key, stale.pub = fs.key, fs.pub
	stale.publish(t, feedG1, catalogApp("shop", "Shop", shopCompose))
	if _, err := stale.replica(t, e).Sync(e.dbCtx(t)); !errors.Is(err, catalogsync.ErrStale) {
		t.Fatalf("a stale mirror: %v", err)
	}
	if got := catalogBySlug(t, owner); len(got) != 2 || got["shop"].LatestRevision != 2 || got["notes"].WithdrawnAt != nil {
		t.Fatalf("after the stale mirror = %+v", got)
	}
	var revs tuc.TemplateView
	owner.get(t, "/api/v1/template/get-template", url.Values{"id": {got["shop"].ID}}).expect(t, http.StatusOK).data(t, &revs)
	if len(revs.Revisions) != 2 {
		t.Fatalf("revisions = %+v", revs.Revisions)
	}
}
