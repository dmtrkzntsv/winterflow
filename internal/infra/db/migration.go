package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	schema "winterflow/internal/infra/db/schema"
	"winterflow/pkg/logger"
)

type Migration struct {
	Version int
	Up      string
	Down    string
}

type Migrator struct {
	db         *sql.DB
	log        *logger.Logger
	migrations []Migration
}

func NewMigrator(log *logger.Logger, db *sql.DB) *Migrator {
	return &Migrator{
		log: log,
		db:  db,
	}
}

func (m *Migrator) LoadMigrations() error {
	files, err := fs.ReadDir(schema.MigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []Migration
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}

		version, err := strconv.Atoi(strings.Split(file.Name(), "_")[0])
		if err != nil {
			m.log.Error("failed to parse migration version", "file", file.Name(), "err", err)
			return fmt.Errorf("invalid migration filename format: %s", file.Name())
		}

		content, err := fs.ReadFile(schema.MigrationsFS, "migrations/"+file.Name())
		if err != nil {
			m.log.Error("failed to read migration file", "file", file.Name(), "err", err)
			return fmt.Errorf("failed to read migration file %s: %w", file.Name(), err)
		}

		parts := strings.Split(string(content), "-- +migrate Down")
		if len(parts) != 2 {
			m.log.Error("failed to parse migration file", "file", file.Name(), "content", string(content))
			return fmt.Errorf("invalid migration format in %s: missing up/down sections", file.Name())
		}

		migrations = append(migrations, Migration{
			Version: version,
			Up:      strings.TrimSpace(strings.TrimPrefix(parts[0], "-- +migrate Up")),
			Down:    strings.TrimSpace(parts[1]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	m.migrations = migrations
	m.log.Info("Loaded migrations", "versions", len(m.migrations))
	return nil
}

func (m *Migrator) Migrate(version ...int) error {
	if err := m.ensureVersionTable(); err != nil {
		return err
	}

	currentVersion, err := m.getCurrentVersion()
	if err != nil {
		return err
	}

	targetVersion := -1
	if len(version) > 0 {
		targetVersion = version[0]
	}

	for _, migration := range m.migrations {
		if migration.Version > currentVersion {
			if targetVersion != -1 && migration.Version > targetVersion {
				break
			}
			m.log.Info("Applying migration", "version", migration.Version)
			if err := m.applyMigration(migration); err != nil {
				m.log.Error("failed to apply migration", "version", migration.Version, "err", err)
				return fmt.Errorf("failed to apply migration %d: %w", migration.Version, err)
			}
		}
	}

	return nil
}

func (m *Migrator) Rollback(version ...int) error {
	currentVersion, err := m.getCurrentVersion()
	if err != nil {
		return err
	}

	targetVersion := -1
	if len(version) > 0 {
		targetVersion = version[0]
	}

	for i := len(m.migrations) - 1; i >= 0; i-- {
		migration := m.migrations[i]
		if migration.Version <= currentVersion {
			if targetVersion != -1 && migration.Version <= targetVersion {
				break
			}
			m.log.Info("Rolling back migration", "version", migration.Version)
			if err := m.rollbackMigration(migration); err != nil {
				m.log.Error("failed to rollback migration", "version", migration.Version, "err", err)
				return fmt.Errorf("failed to rollback migration %d: %w", migration.Version, err)
			}
		}
	}

	return nil
}

func (m *Migrator) ensureVersionTable() error {
	_, err := m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		m.log.Error("failed to ensure schema_migrations table", "err", err)
	}
	return err
}

func (m *Migrator) getCurrentVersion() (int, error) {
	var version int
	err := m.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	return version, err
}

func (m *Migrator) applyMigration(migration Migration) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(migration.Up); err != nil {
		return err
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration.Version); err != nil {
		return err
	}

	return tx.Commit()
}

func (m *Migrator) rollbackMigration(migration Migration) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(migration.Down); err != nil {
		return err
	}

	if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = ?", migration.Version); err != nil {
		return err
	}

	return tx.Commit()
}
