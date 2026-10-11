package repository

import (
	"context"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
)

func newServerDomainRepo(t *testing.T) *DbServerDomainRepository {
	t.Helper()
	conn := newTestDB(t)
	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedOrgWithServer(t, conn, "org-2", "srv-2")
	return NewDbServerDomainRepository(conn, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
}

func TestServerDomainsUpsertListDelete(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "auto"}); err != nil {
		t.Fatal(err)
	}
	// Replacing the certificate updates the row in place.
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "certificate", NotAfter: &notAfter, Issuer: "CF Origin"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}
	// The same name on another server is its own row.
	if err := repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "*.twillingate.app", TLS: "off"}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ListForServer(ctx, "srv-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if got[0].Name != "*.twillingate.app" || got[0].TLS != "certificate" || got[0].Issuer != "CF Origin" ||
		got[0].NotAfter == nil || !got[0].NotAfter.Equal(notAfter) {
		t.Errorf("certificate row = %+v", got[0])
	}
	if got[1].Name != "twillingate.app" || got[1].NotAfter != nil {
		t.Errorf("off row = %+v", got[1])
	}

	if err := repo.Delete(ctx, "srv-1", "twillingate.app"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 || got[0].TLS != "off" {
		t.Fatalf("other server touched: %+v", got)
	}
}

func TestServerDomainsReplaceForServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "old.app", TLS: "auto"})
	_ = repo.Upsert(ctx, "srv-2", model.RegisteredDomain{Name: "keep.app", TLS: "auto"})

	if err := repo.ReplaceForServer(ctx, "srv-1", []model.RegisteredDomain{
		{Name: "a.app", TLS: "auto"}, {Name: "*.b.app", TLS: "off"},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.ListForServer(ctx, "srv-1")
	if len(got) != 2 || got[0].Name != "*.b.app" || got[1].Name != "a.app" {
		t.Fatalf("after replace: %+v", got)
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("empty replace left %+v", got)
	}
	if got, _ := repo.ListForServer(ctx, "srv-2"); len(got) != 1 {
		t.Fatalf("other server touched: %+v", got)
	}
}

// TestServerDomainsCascadeWithServer: removing a server drops its index.
func TestServerDomainsCascadeWithServer(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	_ = repo.Upsert(ctx, "srv-1", model.RegisteredDomain{Name: "a.app", TLS: "auto"})
	if _, err := repo.db.GetDB().NewDelete().Model((*models.Server)(nil)).Where("server_id = ?", "srv-1").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 0 {
		t.Fatalf("rows survived their server: %+v", got)
	}
}

// TestServerDomainsReplaceSkipsUnchanged: an agent reconnect re-reports the
// same registrations; that must not rewrite the rows.
func TestServerDomainsReplaceSkipsUnchanged(t *testing.T) {
	repo := newServerDomainRepo(t)
	ctx := context.Background()
	notAfter := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	rows := []model.RegisteredDomain{
		{Name: "a.app", TLS: model.DomainTLSAuto},
		{Name: "*.b.app", TLS: model.DomainTLSCertificate, NotAfter: &notAfter, Issuer: "CF"},
	}
	if err := repo.ReplaceForServer(ctx, "srv-1", rows); err != nil {
		t.Fatal(err)
	}
	stamps := func() map[string]time.Time {
		var raw []models.ServerDomain
		if err := repo.db.GetDB().NewSelect().Model(&raw).Where("server_id = ?", "srv-1").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		out := map[string]time.Time{}
		for _, r := range raw {
			out[r.Name] = r.UpdatedAt.Time()
		}
		return out
	}
	before := stamps()
	// Force the clock past the stored second, then re-report in another order.
	time.Sleep(1100 * time.Millisecond)
	if err := repo.ReplaceForServer(ctx, "srv-1", []model.RegisteredDomain{rows[1], rows[0]}); err != nil {
		t.Fatal(err)
	}
	after := stamps()
	for name, at := range before {
		if !after[name].Equal(at) {
			t.Errorf("%s rewritten although unchanged: %v -> %v", name, at, after[name])
		}
	}
	// A changed mode is written.
	if err := repo.ReplaceForServer(ctx, "srv-1", []model.RegisteredDomain{{Name: "a.app", TLS: model.DomainTLSOff}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.ListForServer(ctx, "srv-1"); len(got) != 1 || got[0].TLS != model.DomainTLSOff {
		t.Fatalf("changed list not written: %+v", got)
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
