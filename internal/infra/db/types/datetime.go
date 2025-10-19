package types

import (
	"database/sql/driver"
	"fmt"
	"time"
)

type DateTime time.Time

func (dt DateTime) Value() (driver.Value, error) {
	t := time.Time(dt)
	return t.UTC().Format("2006-01-02 15:04:05"), nil
}

func (dt *DateTime) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	switch v := value.(type) {
	case time.Time:
		*dt = DateTime(v)
	case string:
		// Try parsing with the expected format first
		t, err := time.ParseInLocation("2006-01-02 15:04:05", v, time.UTC)
		if err != nil {
			// If that fails, try parsing as a time.Time string
			t, err = time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("failed to parse datetime: %w", err)
			}
		}
		*dt = DateTime(t)
	default:
		return fmt.Errorf("unsupported type for DateTime: %T", value)
	}

	return nil
}

func (dt DateTime) String() string {
	return time.Time(dt).UTC().Format("2006-01-02 15:04:05")
}

func (dt DateTime) Time() time.Time {
	return time.Time(dt)
}

type NullDateTime struct {
	DateTime DateTime
	Valid    bool
}

func (ndt NullDateTime) Value() (driver.Value, error) {
	if !ndt.Valid {
		return nil, nil
	}
	return ndt.DateTime.Value()
}

func (ndt *NullDateTime) Scan(value interface{}) error {
	if value == nil {
		ndt.DateTime, ndt.Valid = DateTime{}, false
		return nil
	}

	ndt.Valid = true
	return ndt.DateTime.Scan(value)
}

func (ndt NullDateTime) String() string {
	if !ndt.Valid {
		return ""
	}
	return ndt.DateTime.String()
}

func (ndt NullDateTime) Time() time.Time {
	if !ndt.Valid {
		return time.Time{}
	}
	return ndt.DateTime.Time()
}
