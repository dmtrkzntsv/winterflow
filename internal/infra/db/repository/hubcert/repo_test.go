package hubcert

import (
	"context"
	"errors"
	"fmt"
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
	var released []string
	release := func(_ context.Context, hub string) error { released = append(released, hub); return nil }
	retire := func(hub, fp string, wantN int, wantReleased ...string) {
		t.Helper()
		released = nil
		n, err := r.Retire(ctx, hub, fp, now, release)
		if err != nil || n != wantN || !reflect.DeepEqual(released, wantReleased) {
			t.Fatalf("Retire(%q, %q) = %d, %v, released %v; want %d, %v", hub, fp, n, err, released, wantN, wantReleased)
		}
	}
	retire("hub-01", "", 1, "hub-01")
	// Again: nothing retired, but the hub is released again (a rerun
	// finishes a failed release).
	retire("hub-01", "", 0, "hub-01")
	// A hub id with no certificate on record (a typo) is not released.
	retire("hub-09", "", 0)
	retire("", "bb", 1, "hub-02")
	retire("", "bb", 0, "hub-02")
	retire("", "zz", 0)
	// A hub with another active certificate left is not released.
	dbtest.Exec(t, d, `INSERT INTO hub_certificates (fingerprint, hub_id, created_at) VALUES (?, ?, ?)`, "c2", "hub-03", now)
	retire("", "c2", 1)
	// A nil release only retires.
	if n, err := r.Retire(ctx, "hub-02", "", now, nil); err != nil || n != 0 {
		t.Fatalf("nil release = %d, %v", n, err)
	}
	// A release error comes back after the retire was committed.
	if err := r.Trust(ctx, "hub-04", "dd", now); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("redis down")
	if n, err := r.Retire(ctx, "hub-04", "", now, func(context.Context, string) error { return boom }); !errors.Is(err, boom) || n != 1 {
		t.Fatalf("failing release = %d, %v", n, err)
	}
	if _, err := r.Retire(ctx, "hub-03", "cc", now, release); err == nil {
		t.Fatal("both filters accepted")
	}
	if _, err := r.Retire(ctx, "", "", now, release); err == nil {
		t.Fatal("no filter accepted")
	}
	if got := active(t, r); !reflect.DeepEqual(got, []string{"cc"}) {
		t.Fatalf("active = %v", got)
	}
}

// The release runs under the per-hub lock: a Trust of the same hub waits
// for it (on SQLite every transaction is serialized anyway).
func TestRetireReleaseHoldsTheHubLock(t *testing.T) {
	r, _ := open(t)
	ctx := context.Background()
	if err := r.Trust(ctx, "hub-01", "aa", now); err != nil {
		t.Fatal(err)
	}
	trusted := make(chan error, 1)
	release := func(context.Context, string) error {
		go func() { trusted <- r.Trust(ctx, "hub-01", "bb", now) }()
		select {
		case err := <-trusted:
			return fmt.Errorf("Trust finished during the release: %v", err)
		case <-time.After(200 * time.Millisecond):
			return nil
		}
	}
	if _, err := r.Retire(ctx, "hub-01", "", now, release); err != nil {
		t.Fatal(err)
	}
	if err := <-trusted; err != nil {
		t.Fatal(err)
	}
	if got := active(t, r); !reflect.DeepEqual(got, []string{"bb"}) {
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
