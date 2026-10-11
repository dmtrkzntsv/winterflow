package types

import (
	"testing"
	"time"
)

func TestDateTimeValueFormatsUTC(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	dt := DateTime(time.Date(2026, 7, 2, 15, 4, 5, 0, loc))
	v, err := dt.Value()
	if err != nil {
		t.Fatal(err)
	}
	if v != "2026-07-02 12:04:05" {
		t.Fatalf("Value() = %q", v)
	}
}

func TestDateTimeScanRoundTrip(t *testing.T) {
	var dt DateTime
	if err := dt.Scan("2026-07-02 12:04:05"); err != nil {
		t.Fatal(err)
	}
	if got := dt.Time().Format("2006-01-02 15:04:05"); got != "2026-07-02 12:04:05" {
		t.Fatalf("scanned = %q", got)
	}
	if dt.String() == "" {
		t.Fatal("String() empty")
	}
}

func TestDateTimeScanTimeValue(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	var dt DateTime
	if err := dt.Scan(now); err != nil {
		t.Fatal(err)
	}
	if !dt.Time().Equal(now) {
		t.Fatalf("scan(time.Time) = %v, want %v", dt.Time(), now)
	}
}

func TestNewDateTimeIsNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	dt := NewDateTime()
	after := time.Now().Add(time.Second)
	if dt.Time().Before(before) || dt.Time().After(after) {
		t.Fatalf("NewDateTime out of range: %v", dt.Time())
	}
}

// Drivers return timestamps in the session's or host's zone (pgx: a fixed
// +00:00 zone, or time.Local); Scan normalizes to UTC so JSON always says Z.
func TestDateTimeScanNormalizesToUTC(t *testing.T) {
	for _, v := range []any{
		time.Date(2026, 7, 2, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*3600)),
		time.Date(2026, 7, 2, 12, 4, 5, 0, time.FixedZone("", 0)),
		"2026-07-02T15:04:05+03:00",
		"2026-07-02 12:04:05",
	} {
		var dt DateTime
		if err := dt.Scan(v); err != nil {
			t.Fatal(err)
		}
		if dt.Time().Location() != time.UTC || !dt.Time().Equal(time.Date(2026, 7, 2, 12, 4, 5, 0, time.UTC)) {
			t.Errorf("Scan(%v) = %v (%v), want 12:04:05 UTC", v, dt.Time(), dt.Time().Location())
		}
	}
}
