package db

import (
	"net/url"
	"testing"
)

func TestUTCDSNPinsTimeZone(t *testing.T) {
	for _, in := range []string{
		"postgres://u:p@db:5432/wf",
		"postgres://u:p@db:5432/wf?sslmode=disable&timezone=America/New_York",
		"postgres://u:p@db/wf?search_path=t_1",
	} {
		out := utcDSN(in)
		u, _ := url.Parse(out)
		q := u.Query()
		if q.Get("timezone") != "UTC" {
			t.Errorf("%s -> %s: timezone not pinned", in, out)
		}
		orig, _ := url.Parse(in)
		for k := range orig.Query() {
			if k != "timezone" && q.Get(k) != orig.Query().Get(k) {
				t.Errorf("%s -> %s: lost %s", in, out, k)
			}
		}
	}
	if bad := "postgres://u:p@db:5432/wf\x7f"; utcDSN(bad) != bad {
		t.Error("an unparsable DSN must pass through unchanged")
	}
}
