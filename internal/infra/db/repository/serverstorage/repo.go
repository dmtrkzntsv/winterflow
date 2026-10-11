// Package serverstorage implements port.ServerStorageRepository: the API's
// index of each agent's storages (server_storages): name, type and location,
// never a secret. The agent's storages/ dir stays authoritative; every method
// here is cache maintenance.
package serverstorage

import (
	"context"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	storageRow   = sqlitegen.ServerStorage
	upsertParams = sqlitegen.UpsertServerStorageParams
	insertParams = sqlitegen.InsertServerStorageParams
	deleteParams = sqlitegen.DeleteServerStorageParams
)

type querier interface {
	ListServerStorages(ctx context.Context, db backend.DBTX, serverID string) ([]storageRow, error)
	UpsertServerStorage(ctx context.Context, db backend.DBTX, p upsertParams) error
	InsertServerStorage(ctx context.Context, db backend.DBTX, p insertParams) error
	DeleteServerStorage(ctx context.Context, db backend.DBTX, p deleteParams) error
	DeleteServerStoragesByServer(ctx context.Context, db backend.DBTX, serverID string) error
}

// Repo is the server_storages repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.ServerStorageRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("serverstorage repository: unknown database driver %q", driver)
}

// ListForServer returns the server's storages sorted by name.
func (r *Repo) ListForServer(ctx context.Context, serverID string) ([]model.StorageSummary, error) {
	rows, err := r.q.ListServerStorages(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make([]model.StorageSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, hydrate(row))
	}
	return out, nil
}

// Upsert records one storage (storage.save confirmed by the agent).
func (r *Repo) Upsert(ctx context.Context, serverID string, s model.StorageSummary) error {
	return r.q.UpsertServerStorage(ctx, r.tx.Querier(ctx), upsertParams(rowFor(serverID, s)))
}

// Delete drops one storage (storage.delete confirmed by the agent).
func (r *Repo) Delete(ctx context.Context, serverID, name string) error {
	return r.q.DeleteServerStorage(ctx, r.tx.Querier(ctx), deleteParams{ServerID: serverID, Name: name})
}

// ReplaceForServer makes the index mirror a storage.list answer. An answer
// equal to the stored rows (every agent reconnect sends one) writes nothing.
func (r *Repo) ReplaceForServer(ctx context.Context, serverID string, ss []model.StorageSummary) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		current, err := r.ListForServer(ctx, serverID)
		if err != nil {
			return err
		}
		if sameStorages(current, ss) {
			return nil
		}
		if err := r.q.DeleteServerStoragesByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		for _, s := range ss {
			if err := r.q.InsertServerStorage(ctx, r.tx.Querier(ctx), rowFor(serverID, s)); err != nil {
				return err
			}
		}
		return nil
	})
}

// rowFor is the row a storage is stored as, updated now.
func rowFor(serverID string, s model.StorageSummary) insertParams {
	return insertParams{
		ServerID:  serverID,
		Name:      s.Name,
		Type:      string(s.Type),
		Location:  s.Location,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
}

func hydrate(row storageRow) model.StorageSummary {
	return model.StorageSummary{Name: row.Name, Type: model.StorageType(row.Type), Location: row.Location}
}

// sameStorages reports whether two lists hold the same rows, in any order.
func sameStorages(a, b []model.StorageSummary) bool {
	if len(a) != len(b) {
		return false
	}
	byName := make(map[string]model.StorageSummary, len(a))
	for _, s := range a {
		byName[s.Name] = s
	}
	for _, s := range b {
		if o, ok := byName[s.Name]; !ok || o != s {
			return false
		}
	}
	return true
}
