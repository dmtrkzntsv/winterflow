package sharedvar

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func varRows(rows []pgsqlgen.SharedVariable) []varRow {
	out := make([]varRow, len(rows))
	for i, row := range rows {
		out[i] = varRow(row)
	}
	return out
}

func (pgsqlQuerier) ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgSharedVariables(ctx, orgID)
	return varRows(rows), err
}

func (pgsqlQuerier) ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error) {
	rows, err := pgsqlgen.New(db).ListServerSharedVariables(ctx, serverID)
	return varRows(rows), err
}

func (pgsqlQuerier) GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error) {
	row, err := pgsqlgen.New(db).GetSharedVariable(ctx, id)
	return varRow(row), err
}

func (pgsqlQuerier) GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error) {
	row, err := pgsqlgen.New(db).GetOrgSharedVariableByName(ctx, pgsqlgen.GetOrgSharedVariableByNameParams(p))
	return varRow(row), err
}

func (pgsqlQuerier) GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error) {
	row, err := pgsqlgen.New(db).GetServerSharedVariableByName(ctx, pgsqlgen.GetServerSharedVariableByNameParams(p))
	return varRow(row), err
}

func (pgsqlQuerier) InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertSharedVariable(ctx, pgsqlgen.InsertSharedVariableParams(p))
}

func (pgsqlQuerier) UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error {
	return pgsqlgen.New(db).UpdateSharedVariable(ctx, pgsqlgen.UpdateSharedVariableParams(p))
}

func (pgsqlQuerier) DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error {
	return pgsqlgen.New(db).DeleteSharedVariable(ctx, id)
}

func (pgsqlQuerier) ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error) {
	rows, err := pgsqlgen.New(db).ListCiphertextServersByOrg(ctx, orgID)
	out := make([]ctServerRow, len(rows))
	for i, row := range rows {
		out[i] = ctServerRow(row)
	}
	return out, err
}

func (pgsqlQuerier) ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error) {
	rows, err := pgsqlgen.New(db).ListSharedCiphertextsForServer(ctx, serverID)
	out := make([]ctRow, len(rows))
	for i, row := range rows {
		out[i] = ctRow(row)
	}
	return out, err
}

func (pgsqlQuerier) UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error {
	return pgsqlgen.New(db).UpsertSharedCiphertext(ctx, pgsqlgen.UpsertSharedCiphertextParams(p))
}

func (pgsqlQuerier) DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error {
	return pgsqlgen.New(db).DeleteSharedCiphertextsByVariable(ctx, variableID)
}

func (pgsqlQuerier) DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error {
	return pgsqlgen.New(db).DeleteSharedCiphertext(ctx, pgsqlgen.DeleteSharedCiphertextParams(p))
}

func (pgsqlQuerier) DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteSharedCiphertextsByServer(ctx, serverID)
}

func (pgsqlQuerier) BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).BumpServerVarsRevision(ctx, serverID)
}

func (pgsqlQuerier) GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error) {
	row, err := pgsqlgen.New(db).GetServerVarsInfo(ctx, serverID)
	return infoRow(row), err
}

func (pgsqlQuerier) ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgServers(ctx, orgID)
	out := make([]orgServerRow, len(rows))
	for i, row := range rows {
		out[i] = orgServerRow(row)
	}
	return out, err
}

func (pgsqlQuerier) ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgPublicKeys(ctx, orgID)
	out := make([]pubKeyRow, len(rows))
	for i, row := range rows {
		out[i] = pubKeyRow(row)
	}
	return out, err
}
