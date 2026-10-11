package serverdomain

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustUpsert(t *testing.T, ctx context.Context, r *Repo, serverID string, d model.RegisteredDomain) {
	t.Helper()
	if err := r.Upsert(ctx, serverID, d); err != nil {
		t.Fatal(err)
	}
}

func mustList(t *testing.T, ctx context.Context, r *Repo, serverID string) []model.RegisteredDomain {
	t.Helper()
	got, err := r.ListForServer(ctx, serverID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestServerDomainsUpsertListDelete(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	// Names differing only in punctuation ("*.x" vs "x") order differently
	// under SQLite's binary and Postgres's locale collation, so the two
	// names here are separated by a letter.
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "a.twillingate.app", TLS: "auto"})
	// Replacing the certificate updates the row in place.
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "a.twillingate.app", TLS: "certificate", NotAfter: &notAfter, Issuer: "CF Origin"})
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "b.twillingate.app", TLS: "off"})
	// The same name on another server is its own row.
	mustUpsert(t, ctx, repo, srv2, model.RegisteredDomain{Name: "a.twillingate.app", TLS: "off"})

	got := mustList(t, ctx, repo, srv1)
	if len(got) != 2 {
		t.Fatalf("list = %+v", got)
	}
	if got[0].Name != "a.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF Origin" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(notAfter) || got[0].NotAfter.Location() != time.UTC {
		t.Errorf("certificate row = %+v", got[0])
	}
	if got[1].Name != "b.twillingate.app" || got[1].NotAfter != nil {
		t.Errorf("off row = %+v", got[1])
	}

	if err := repo.Delete(ctx, srv1, "b.twillingate.app"); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got := mustList(t, ctx, repo, srv2); len(got) != 1 || got[0].TLS != "off" {
		t.Fatalf("other server touched: %+v", got)
	}
	// Deleting what is not there is silent.
	if err := repo.Delete(ctx, srv1, "missing.app"); err != nil {
		t.Fatal(err)
	}
}

func TestServerDomainsListEmptyIsNonNil(t *testing.T) {
	repo, _ := newRepo(t)
	if got := mustList(t, dbtest.Ctx(t), repo, srv1); got == nil || len(got) != 0 {
		t.Fatalf("empty list = %#v, want non-nil empty slice", got)
	}
}

func TestServerDomainsReplaceForServer(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	mustUpsert(t, ctx, repo, srv2, model.RegisteredDomain{Name: "keep.app", TLS: "auto"})

	// Names whose order is the same under SQLite's binary collation and a
	// Postgres locale collation (which ignores punctuation such as "*.").
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{
		{Name: "b.app", TLS: "off"}, {Name: "a.app", TLS: "auto"},
	}); err != nil {
		t.Fatal(err)
	}
	got := mustList(t, ctx, repo, srv1)
	if len(got) != 2 || got[0].Name != "a.app" || got[1].Name != "b.app" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, srv1, nil); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got := mustList(t, ctx, repo, srv2); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// A failing insert rolls the whole replace back: the old rows survive.
func TestServerDomainsReplaceIsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	dup := []model.RegisteredDomain{{Name: "x.app", TLS: "auto"}, {Name: "x.app", TLS: "off"}}
	if err := repo.ReplaceForServer(ctx, srv1, dup); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 || got[0].Name != "old.app" {
		t.Fatalf("after failed replace: %+v", got)
	}
}

// TestServerDomainsCascadeWithServer: removing a server drops its index.
func TestServerDomainsCascadeWithServer(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "a.app", TLS: "auto"})
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv1)
	if got := mustList(t, ctx, repo, srv1); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}

// TestServerDomainsReplaceSkipsUnchanged: an agent reconnect re-reports the
// same registrations; that must not rewrite the rows.
func TestServerDomainsReplaceSkipsUnchanged(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []model.RegisteredDomain{
		{Name: "a.app", TLS: model.DomainTLSAuto},
		{Name: "*.b.app", TLS: model.DomainTLSCertificate, NotAfter: &notAfter, Issuer: "CF"},
	}
	if err := repo.ReplaceForServer(ctx, srv1, rows); err != nil {
		t.Fatal(err)
	}
	stamps := func() map[string]string {
		out := map[string]string{}
		for _, name := range []string{"a.app", "*.b.app"} {
			out[name] = dbtest.QueryString(t, d, `SELECT CAST(updated_at AS TEXT) FROM server_domains WHERE server_id = ? AND name = ?`, srv1, name)
		}
		return out
	}
	before := stamps()
	// Force the clock past the stored second, then re-report in another order.
	time.Sleep(1100 * time.Millisecond)
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{rows[1], rows[0]}); err != nil {
		t.Fatal(err)
	}
	after := stamps()
	for name, at := range before {
		if after[name] != at {
			t.Errorf("%s rewritten although unchanged: %v -> %v", name, at, after[name])
		}
	}
	// A changed mode is written.
	if err := repo.ReplaceForServer(ctx, srv1, []model.RegisteredDomain{{Name: "a.app", TLS: model.DomainTLSOff}}); err != nil {
		t.Fatal(err)
	}
	if got := mustList(t, ctx, repo, srv1); len(got) != 1 || got[0].TLS != model.DomainTLSOff {
		t.Fatalf("changed list not written: %+v", got)
	}
}

// Timestamps keep the Bun-era storage format: naive UTC
// "2006-01-02 15:04:05" text (TIMESTAMP on both engines here).
func TestServerDomainsStoredTimestampFormat(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+3", 3*3600))
	mustUpsert(t, ctx, repo, srv1, model.RegisteredDomain{Name: "a.app", TLS: "certificate", NotAfter: &notAfter})
	if got := dbtest.QueryString(t, d, `SELECT CAST(not_after AS TEXT) FROM server_domains WHERE name = 'a.app'`); got != "2027-01-02 00:04:05" {
		t.Fatalf("stored not_after = %q, want 2027-01-02 00:04:05", got)
	}
}

func TestSameDomains(t *testing.T) {
	t1 := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	d := func(name string, tls model.DomainTLS, na *time.Time, issuer string) model.RegisteredDomain {
		return model.RegisteredDomain{Name: name, TLS: tls, NotAfter: na, Issuer: issuer}
	}
	base := []model.RegisteredDomain{d("a.app", "auto", nil, ""), d("b.app", "certificate", &t1, "CF")}
	cases := map[string]struct {
		other []model.RegisteredDomain
		same  bool
	}{
		"reordered":   {[]model.RegisteredDomain{base[1], base[0]}, true},
		"fewer":       {base[:1], false},
		"renamed":     {[]model.RegisteredDomain{base[0], d("c.app", "certificate", &t1, "CF")}, false},
		"mode":        {[]model.RegisteredDomain{d("a.app", "off", nil, ""), base[1]}, false},
		"issuer":      {[]model.RegisteredDomain{base[0], d("b.app", "certificate", &t1, "LE")}, false},
		"expiry":      {[]model.RegisteredDomain{base[0], d("b.app", "certificate", &t2, "CF")}, false},
		"expiry gone": {[]model.RegisteredDomain{base[0], d("b.app", "certificate", nil, "CF")}, false},
		"sub-second":  {[]model.RegisteredDomain{base[0], d("b.app", "certificate", ptr(t1.Add(time.Millisecond)), "CF")}, true},
	}
	for name, tc := range cases {
		if got := sameDomains(base, tc.other); got != tc.same {
			t.Errorf("%s: sameDomains = %v, want %v", name, got, tc.same)
		}
	}
}

func ptr[T any](v T) *T { return &v }
