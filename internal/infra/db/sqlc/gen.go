// Package sqlc holds the sqlc configuration (sqlc.yaml) and the hand-written
// per-engine queries (query/sqlite, query/pgsql). `make sqlc` regenerates
// gen/sqlite (package sqlitegen) and gen/pgsql (package pgsqlgen); the
// generated code is committed and `make sqlc-check` fails when it is stale.
package sqlc

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
