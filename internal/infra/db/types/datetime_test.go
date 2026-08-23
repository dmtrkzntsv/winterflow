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
