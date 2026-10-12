package sharedvar

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error) {
	return sqlitegen.New(db).ListOrgSharedVariables(ctx, orgID)
}

func (sqliteQuerier) ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error) {
	return sqlitegen.New(db).ListServerSharedVariables(ctx, serverID)
}

func (sqliteQuerier) GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error) {
	return sqlitegen.New(db).GetSharedVariable(ctx, id)
}

func (sqliteQuerier) GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error) {
	return sqlitegen.New(db).GetOrgSharedVariableByName(ctx, p)
}

func (sqliteQuerier) GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error) {
	return sqlitegen.New(db).GetServerSharedVariableByName(ctx, p)
}

func (sqliteQuerier) InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertSharedVariable(ctx, p)
}

func (sqliteQuerier) UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error {
	return sqlitegen.New(db).UpdateSharedVariable(ctx, p)
}

func (sqliteQuerier) DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error {
	return sqlitegen.New(db).DeleteSharedVariable(ctx, id)
}

func (sqliteQuerier) ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error) {
	return sqlitegen.New(db).ListCiphertextServersByOrg(ctx, orgID)
}

func (sqliteQuerier) ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error) {
	return sqlitegen.New(db).ListSharedCiphertextsForServer(ctx, serverID)
}

func (sqliteQuerier) UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error {
	return sqlitegen.New(db).UpsertSharedCiphertext(ctx, p)
}

func (sqliteQuerier) DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error {
	return sqlitegen.New(db).DeleteSharedCiphertextsByVariable(ctx, variableID)
}

func (sqliteQuerier) DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error {
	return sqlitegen.New(db).DeleteSharedCiphertext(ctx, p)
}

func (sqliteQuerier) DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteSharedCiphertextsByServer(ctx, serverID)
}

func (sqliteQuerier) BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).BumpServerVarsRevision(ctx, serverID)
}

func (sqliteQuerier) GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error) {
	return sqlitegen.New(db).GetServerVarsInfo(ctx, serverID)
}

func (sqliteQuerier) ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error) {
	return sqlitegen.New(db).ListOrgServers(ctx, orgID)
}

func (sqliteQuerier) ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error) {
	return sqlitegen.New(db).ListOrgPublicKeys(ctx, orgID)
}
