// Package pgsql opens Postgres databases with jackc/pgx/v5 behind
// database/sql.
package pgsql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Scheme prefixes a Postgres DATABASE_URL.
const Scheme = "postgres://"

// Open connects to a postgres:// URL with the session time zone pinned to
// UTC, sets the pool limits and pings within 10 s. It does not migrate.
//
// pgx runs in simple-protocol mode: arguments are interpolated client-side
// as Bun's driver did, nothing is prepared server-side (so PgBouncer in
// transaction mode keeps working), and a types.DateTime travels as its
// "2006-01-02 15:04:05" text, which the UTC session reads as UTC.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(utcDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PostgreSQL connection string: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL database: %w", err)
	}
	return db, nil
}

// utcDSN pins the session time zone to UTC. types.DateTime writes naive
// UTC wall-clock strings ("2006-01-02 15:04:05"); a TIMESTAMPTZ column
// reads those in the session zone, so any other server/role time zone would
// shift every stored timestamp by its offset. pgx sends unknown query
// parameters as startup parameters. An unparsable DSN is returned unchanged
// for pgx to reject with its own error.
func utcDSN(connStr string) string {
	u, err := url.Parse(connStr)
	if err != nil {
		return connStr
	}
	q := u.Query()
	for k := range q {
		// A user's differently-cased "TimeZone=" must not be sent
		// alongside ours.
		if strings.EqualFold(k, "timezone") {
			q.Del(k)
		}
	}
	q.Set("timezone", "UTC")
	u.RawQuery = q.Encode()
	return u.String()
}
