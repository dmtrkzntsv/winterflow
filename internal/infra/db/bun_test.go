package db

import (
	"net/url"
	"strings"
	"testing"
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
