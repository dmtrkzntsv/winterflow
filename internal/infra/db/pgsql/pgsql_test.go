package pgsql

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUTCDSNPinsTimeZone(t *testing.T) {
	for _, in := range []string{
		"postgres://u:p@db:5432/wf",
		"postgres://u:p@db:5432/wf?sslmode=disable&timezone=America/New_York",
		"postgres://u:p@db:5432/wf?TimeZone=Europe/Berlin&TIMEZONE=Asia/Tokyo",
		"postgres://u:p@db/wf?search_path=t_1",
		"postgres://u:p%40%2F%3A%23@db:5432/wf",
		"postgres://u:p@/wf?host=/var/run/postgresql",
		"postgres://u:p@[2001:db8::1]:5432/wf?sslmode=disable",
	} {
		out := utcDSN(in)
		u, err := url.Parse(out)
		if err != nil {
			t.Fatalf("%s -> %s: %v", in, out, err)
		}
		q := u.Query()
		if q.Get("timezone") != "UTC" {
			t.Errorf("%s -> %s: timezone not pinned", in, out)
		}
		for k := range q {
			if k != "timezone" && strings.EqualFold(k, "timezone") {
				t.Errorf("%s -> %s: stray %s", in, out, k)
			}
		}
		orig, _ := url.Parse(in)
		if u.Scheme != orig.Scheme || u.Host != orig.Host || u.Path != orig.Path {
			t.Errorf("%s -> %s: scheme/host/path changed", in, out)
		}
		ouser := orig.User.Username()
		opass, _ := orig.User.Password()
		nuser := u.User.Username()
		npass, _ := u.User.Password()
		if ouser != nuser || opass != npass {
			t.Errorf("%s -> %s: userinfo changed (%q:%q -> %q:%q)", in, out, ouser, opass, nuser, npass)
		}
		for k := range orig.Query() {
			if !strings.EqualFold(k, "timezone") && q.Get(k) != orig.Query().Get(k) {
				t.Errorf("%s -> %s: lost %s", in, out, k)
			}
		}
	}
	if bad := "postgres://u:p@db:5432/wf\x7f"; utcDSN(bad) != bad {
		t.Error("an unparsable DSN must pass through unchanged")
	}
}

// TestOpenPinsUTCSession: whatever zone the URL asks for, the session runs
// in UTC, so naive "2006-01-02 15:04:05" timestamps are stored as UTC.
func TestOpenPinsUTCSession(t *testing.T) {
	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("needs WF_TEST_DATABASE_URL (make test-pg)")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme = "postgres"
	q := u.Query()
	q.Set("TimeZone", "America/New_York")
	u.RawQuery = q.Encode()
	db, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tz string
	if err := db.QueryRow(`SHOW timezone`).Scan(&tz); err != nil || tz != "UTC" {
		t.Fatalf("session timezone = %q, %v", tz, err)
	}
	var got time.Time
	if err := db.QueryRow(`SELECT $1::timestamptz`, "2026-07-02 12:04:05").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 7, 2, 12, 4, 5, 0, time.UTC)) {
		t.Fatalf("naive timestamp read back as %v", got)
	}
}

func TestOpenFailsFast(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := Open(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=2"); err == nil {
		t.Fatal("Open succeeded against a closed port")
	}
	if _, err := Open(ctx, "postgres://u:p@db:5432/wf\x7f"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("unparsable DSN: err = %v", err)
	}
}
