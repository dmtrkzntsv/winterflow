package repository

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"

	"github.com/uptrace/bun"
)

func NewDbServerStorageRepository(db *db.BunConnection, log *logger.Logger) *DbServerStorageRepository {
	return &DbServerStorageRepository{db: db, log: log}
}

// DbServerStorageRepository maintains the server_storages index. The agent's
// storages/ dir stays authoritative; every method here is cache maintenance.
type DbServerStorageRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

func serverStorageRow(serverID string, s model.StorageSummary) models.ServerStorage {
	return models.ServerStorage{
		ServerID: serverID, Name: s.Name, Type: string(s.Type), Location: s.Location,
		UpdatedAt: types.DateTime(time.Now().UTC()),
	}
}

// ListForServer returns the server's storages sorted by name.
func (r *DbServerStorageRepository) ListForServer(ctx context.Context, serverID string) ([]model.StorageSummary, error) {
	return listServerStorages(ctx, r.db.GetDB(), serverID)
}

func listServerStorages(ctx context.Context, db bun.IDB, serverID string) ([]model.StorageSummary, error) {
	var rows []models.ServerStorage
	if err := db.NewSelect().Model(&rows).
		Where("server_id = ?", serverID).Order("name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]model.StorageSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.StorageSummary{Name: row.Name, Type: model.StorageType(row.Type), Location: row.Location})
	}
	return out, nil
}

// Upsert records one storage (storage.save confirmed by the agent).
func (r *DbServerStorageRepository) Upsert(ctx context.Context, serverID string, s model.StorageSummary) error {
	row := serverStorageRow(serverID, s)
	_, err := r.db.GetDB().NewInsert().Model(&row).
		On("CONFLICT (server_id, name) DO UPDATE").
		Set("type = EXCLUDED.type").
		Set("location = EXCLUDED.location").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	return err
}

// Delete drops one storage (storage.delete confirmed by the agent).
func (r *DbServerStorageRepository) Delete(ctx context.Context, serverID, name string) error {
	_, err := r.db.GetDB().NewDelete().Model((*models.ServerStorage)(nil)).
		Where("server_id = ?", serverID).Where("name = ?", name).Exec(ctx)
	return err
}

// ReplaceForServer makes the index mirror a storage.list answer. An answer
// equal to the stored rows (every agent reconnect sends one) writes nothing.
func (r *DbServerStorageRepository) ReplaceForServer(ctx context.Context, serverID string, ss []model.StorageSummary) error {
	return r.db.GetDB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current, err := listServerStorages(ctx, tx, serverID)
		if err != nil {
			return err
		}
		if sameStorages(current, ss) {
			return nil
		}
		if _, err := tx.NewDelete().Model((*models.ServerStorage)(nil)).Where("server_id = ?", serverID).Exec(ctx); err != nil {
			return err
		}
		if len(ss) == 0 {
			return nil
		}
		rows := make([]models.ServerStorage, 0, len(ss))
		for _, s := range ss {
			rows = append(rows, serverStorageRow(serverID, s))
		}
		_, err = tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
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
