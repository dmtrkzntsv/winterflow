package hubcert

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

func open(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func active(t *testing.T, r *Repo) []string {
	t.Helper()
	got, err := r.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

var now = time.Now().UTC().Truncate(time.Second)

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql"); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestActiveEmptyIsNotNil(t *testing.T) {
	r, _ := open(t)
	if got := active(t, r); got == nil || len(got) != 0 {
		t.Fatalf("active = %#v, want empty non-nil", got)
	}
}

func TestTrustIdempotentAndRetiresSiblings(t *testing.T) {
	r, d := open(t)
	ctx := context.Background()
	for _, c := range []struct{ hub, fp string }{{"hub-01", "aa"}, {"hub-02", "bb"}, {"hub-01", "aa"}} {
		if err := r.Trust(ctx, c.hub, c.fp, now); err != nil {
			t.Fatal(err)
		}
	}
	if got := active(t, r); !reflect.DeepEqual(got, []string{"aa", "bb"}) {
		t.Fatalf("active = %v", got)
	}
	// A new certificate for hub-01 retires "aa"; hub-02's stays.
	if err := r.Trust(ctx, "hub-01", "cc", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := active(t, r); !reflect.DeepEqual(got, []string{"bb", "cc"}) {
		t.Fatalf("active = %v", got)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM hub_certificates`); n != "3" {
		t.Fatalf("%s rows, want 3", n)
	}
	// A retired certificate is never trusted again.
	if err := r.Trust(ctx, "hub-01", "aa", now); !errors.Is(err, port.ErrHubCertRetired) {
		t.Fatalf("retired = %v", err)
	}
	if err := r.Trust(ctx, "hub-09", "bb", now); !errors.Is(err, port.ErrHubCertOtherHub) {
		t.Fatalf("other hub = %v", err)
	}
	// The refused calls rolled back: nothing else changed.
	if got := active(t, r); !reflect.DeepEqual(got, []string{"bb", "cc"}) {
		t.Fatalf("active = %v", got)
	}
	if err := r.Trust(ctx, "", "dd", now); err == nil {
		t.Fatal("empty hub id accepted")
	}
	if err := r.Trust(ctx, "hub-01", "", now); err == nil {
		t.Fatal("empty fingerprint accepted")
	}
}

func TestRetire(t *testing.T) {
	r, d := open(t)
	ctx := context.Background()
	for _, c := range []struct{ hub, fp string }{{"hub-01", "aa"}, {"hub-02", "bb"}, {"hub-03", "cc"}} {
		if err := r.Trust(ctx, c.hub, c.fp, now); err != nil {
			t.Fatal(err)
		}
	}
	retire := func(hub, fp string, wantN int, wantOrphaned []string) {
		t.Helper()
		n, orphaned, err := r.Retire(ctx, hub, fp, now)
		if err != nil || n != wantN || !reflect.DeepEqual(orphaned, wantOrphaned) {
			t.Fatalf("Retire(%q, %q) = %d, %v, %v; want %d, %v", hub, fp, n, orphaned, err, wantN, wantOrphaned)
		}
	}
	retire("hub-01", "", 1, []string{"hub-01"})
	// Again: nothing retired, but the hub is still reported (a rerun
	// finishes the cleanup).
	retire("hub-01", "", 0, []string{"hub-01"})
	retire("", "bb", 1, []string{"hub-02"})
	retire("", "bb", 0, []string{"hub-02"})
	retire("", "zz", 0, nil)
	// A hub with another active certificate left is not orphaned.
	dbtest.Exec(t, d, `INSERT INTO hub_certificates (fingerprint, hub_id, created_at) VALUES (?, ?, ?)`, "c2", "hub-03", now)
	retire("", "c2", 1, nil)
	if _, _, err := r.Retire(ctx, "hub-03", "cc", now); err == nil {
		t.Fatal("both filters accepted")
	}
	if _, _, err := r.Retire(ctx, "", "", now); err == nil {
		t.Fatal("no filter accepted")
	}
	if got := active(t, r); !reflect.DeepEqual(got, []string{"cc"}) {
		t.Fatalf("active = %v", got)
	}
}

func TestTrustConcurrentSameHub(t *testing.T) {
	r, _ := open(t)
	ctx := context.Background()
	fps := []string{"f1", "f2", "f3", "f4"}
	errs := make(chan error, len(fps))
	for _, fp := range fps {
		go func() { errs <- r.Trust(ctx, "hub-01", fp, now) }()
	}
	for range fps {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// However they interleave, exactly one certificate of the hub stays active.
	if got := active(t, r); len(got) != 1 {
		t.Fatalf("active = %v, want exactly one", got)
	}
}
