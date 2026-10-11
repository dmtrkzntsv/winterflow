//go:build freezedump

// TestFreezeBunDumps writes the frozen Bun-era databases the migration
// runner's adoption tests load (internal/infra/db/migrate/testdata). It was
// run once, before Bun was removed, with:
//
//	WF_TEST_DATABASE_URL=postgres://... go test -tags freezedump -run TestFreezeBunDumps -count=1 ./internal/infra/db/
//
// Each dump is every statement Bun executed (DDL, its bun_migrations
// bookkeeping, then seed rows written through the Bun models), minus SELECTs
// and transaction control, so replaying it rebuilds the database exactly.
package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/migrate"

	"winterflow/internal/infra/db/migrations"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// recorder keeps every successful statement Bun sends that changes the
// database, in order.
type recorder struct{ stmts []string }

func (r *recorder) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context { return ctx }

func (r *recorder) AfterQuery(_ context.Context, e *bun.QueryEvent) {
	q := strings.TrimSpace(e.Query)
	if e.Err != nil || q == "" {
		return
	}
	switch strings.ToUpper(strings.Fields(q)[0]) {
	case "SELECT", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE":
		return
	}
	r.stmts = append(r.stmts, q)
}

// Fixed ids, so the adoption tests can look the seed rows up.
const (
	fzOrg   = "0192a3b4-0000-7000-8000-000000000001"
	fzUser  = "0192a3b4-0000-7000-8000-000000000002"
	fzSrv   = "0192a3b4-0000-7000-8000-000000000003"
	fzCert  = "0192a3b4-0000-7000-8000-000000000004"
	fzApp   = "0192a3b4-0000-7000-8000-000000000005"
	fzTok   = "0192a3b4-0000-7000-8000-000000000006"
	fzReg   = "0192a3b4-0000-7000-8000-000000000007"
	fzRegCt = "0192a3b4-0000-7000-8000-000000000008"
)

// seed writes one row into every table through the Bun models, the way the
// Bun repositories wrote them. withServerDomains: server_domains exists.
func seed(t *testing.T, ctx context.Context, db *bun.DB, withServerDomains bool) {
	t.Helper()
	at := types.DateTime(time.Date(2026, 8, 23, 10, 11, 12, 0, time.UTC))
	exp := types.DateTime(time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC))
	avatar := "https://example.test/a.png"
	rows := []any{
		&models.Organization{OrganizationID: fzOrg, Name: "frozen org", Icon: "server", Color: "#3b82f6", CreatedAt: at},
		&models.User{UserID: fzUser, Name: "Frozen", Avatar: &avatar, CreatedAt: at, LastSeen: at},
		&models.OrganizationUser{OrganizationID: fzOrg, UserID: fzUser, Role: "owner", CreatedAt: at},
		&models.UserConnectedAccount{Provider: "local", ExternalID: "frozen@example.test", UserID: fzUser},
		&models.UserCredentials{UserID: fzUser, Email: "frozen@example.test", PasswordHash: "$2a$10$frozenfrozenfrozenfrozenfrozenfrozenfrozenfrozenfroze", MustChangePassword: true, UpdatedAt: at},
		// A ';' inside a string literal exercises the replay's splitter.
		&models.UserToken{TokenID: fzTok, UserID: fzUser, Name: "ci; deploy", TokenPrefix: "wfp_abcd", TokenHash: strings.Repeat("ab", 32), TokenType: "pat", ExpiresAt: &exp, CreatedAt: at},
		&models.Server{ServerID: fzSrv, OrganizationID: fzOrg, Name: "box", CreatedAt: at, LastSeen: &at},
		&models.ServerCapability{ServerID: fzSrv, Name: "server_ip", Value: "203.0.113.7", UpdatedAt: at},
		&models.ServerFeature{ServerID: fzSrv, Name: "ingress", IsEnabled: true},
		&models.ServerCertificate{CertificateID: fzCert, ServerID: fzSrv, Certificate: "Y2VydA==", IsActive: true, ExpiresAt: exp, CreatedAt: at},
		&models.ServerRegistration{ServerID: fzReg, CertificateID: fzRegCt, Hostname: "pending", Code: "123456", ExpiresAt: exp, Certificate: "Y2VydA==", CertificateExpiresAt: exp, CreatedAt: at},
		&models.App{AppID: fzApp, ServerID: fzSrv, Name: "grafana", Version: "3", Icon: "chart", Color: "#123456", CreatedAt: at},
		&models.AppDomain{Domain: "grafana.example.test", AppID: fzApp, ServerID: fzSrv, Kind: "route", SSL: true, UpstreamPort: 3000, UpdatedAt: at},
		&models.AppDomain{Domain: "www.example.test", AppID: fzApp, ServerID: fzSrv, Kind: "redirect", Target: "https://grafana.example.test", Code: 301, UpdatedAt: at},
	}
	if withServerDomains {
		rows = append(rows,
			&models.ServerDomain{ServerID: fzSrv, Name: "*.example.test", TLS: "certificate", NotAfter: &exp, Issuer: "CF Origin", UpdatedAt: at},
			&models.ServerDomain{ServerID: fzSrv, Name: "example.test", TLS: "auto", UpdatedAt: at},
		)
	}
	for _, r := range rows {
		if _, err := db.NewInsert().Model(r).Exec(ctx); err != nil {
			t.Fatalf("seed %T: %v", r, err)
		}
	}
}

// migrateUpTo applies the Bun migrations named <= last ("" = all).
func migrateUpTo(t *testing.T, ctx context.Context, db *bun.DB, last string) {
	t.Helper()
	set := migrate.NewMigrations()
	for _, m := range migrations.Migrations.Sorted() {
		if last == "" || m.Name <= last {
			set.Add(m)
		}
	}
	mg := migrate.NewMigrator(db, set)
	if err := mg.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mg.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func freeze(t *testing.T, engine string, open func(t *testing.T) *bun.DB) {
	t.Helper()
	for _, c := range []struct{ file, last string }{
		{"bun_full.sql", ""},
		{"bun_20260711000001.sql", "20260711000001"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		db := open(t)
		rec := &recorder{}
		db.AddQueryHook(rec)
		migrateUpTo(t, ctx, db, c.last)
		seed(t, ctx, db, c.last == "")

		var b strings.Builder
		b.WriteString("-- Frozen from the Bun-era code by internal/infra/db/freeze_dump_test.go\n")
		b.WriteString("-- (every statement Bun executed except SELECTs). Do not edit.\n")
		for _, s := range rec.stmts {
			b.WriteString(s)
			b.WriteString(";\n")
		}
		dir := filepath.Join("migrate", "testdata", engine)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreezeBunDumps(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "freeze"})
	freeze(t, "sqlite", func(t *testing.T) *bun.DB {
		sqldb, err := setupSQLite("sqlite://"+filepath.Join(t.TempDir(), "freeze.sqlite"), log)
		if err != nil {
			t.Fatal(err)
		}
		db := bun.NewDB(sqldb, sqlitedialect.New())
		t.Cleanup(func() { _ = db.Close() })
		return db
	})

	base := os.Getenv("WF_TEST_DATABASE_URL")
	if base == "" {
		t.Fatal("WF_TEST_DATABASE_URL must name a Postgres server for the pgsql dumps")
	}
	freeze(t, "pgsql", func(t *testing.T) *bun.DB {
		var rb [6]byte
		if _, err := rand.Read(rb[:]); err != nil {
			t.Fatal(err)
		}
		schema := "freeze_" + hex.EncodeToString(rb[:])
		admin, err := setupPostgreSQL(base, log)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			_ = admin.Close()
		})
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		sqldb, err := setupPostgreSQL(utcDSN(u.String()), log)
		if err != nil {
			t.Fatal(err)
		}
		db := bun.NewDB(sqldb, pgdialect.New())
		t.Cleanup(func() { _ = db.Close() })
		return db
	})
}
