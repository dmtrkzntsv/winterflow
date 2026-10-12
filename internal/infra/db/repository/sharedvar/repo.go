// Package sharedvar implements port.SharedVariableRepository over the
// shared_variables and shared_secret_ciphertexts tables (and
// servers.vars_revision).
package sharedvar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	varRow         = sqlitegen.SharedVariable
	insertParams   = sqlitegen.InsertSharedVariableParams
	updateParams   = sqlitegen.UpdateSharedVariableParams
	orgNameParams  = sqlitegen.GetOrgSharedVariableByNameParams
	srvNameParams  = sqlitegen.GetServerSharedVariableByNameParams
	ctServerRow    = sqlitegen.ListCiphertextServersByOrgRow
	ctRow          = sqlitegen.ListSharedCiphertextsForServerRow
	upsertCtParams = sqlitegen.UpsertSharedCiphertextParams
	deleteCtParams = sqlitegen.DeleteSharedCiphertextParams
	infoRow        = sqlitegen.GetServerVarsInfoRow
	raiseParams    = sqlitegen.RaiseServerVarsRevisionParams
	orgServerRow   = sqlitegen.ListOrgServersRow
	pubKeyRow      = sqlitegen.ListOrgPublicKeysRow
)

type querier interface {
	ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error)
	ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error)
	GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error)
	GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error)
	GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error)
	InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error
	UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error
	DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error
	ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error)
	ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error)
	UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error
	DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error
	DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error
	DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error
	BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error
	RaiseServerVarsRevision(ctx context.Context, db backend.DBTX, p raiseParams) error
	GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error)
	ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error)
	ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error)
}

// Repo is the shared variables repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.SharedVariableRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("sharedvar repository: unknown database driver %q", driver)
}

func (r *Repo) ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error) {
	rows, err := r.q.ListOrgSharedVariables(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	return r.withCipherServers(ctx, orgID, rows)
}

func (r *Repo) ListServer(ctx context.Context, serverID string) ([]model.SharedVariable, error) {
	id := serverID
	rows, err := r.q.ListServerSharedVariables(ctx, r.tx.Querier(ctx), &id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []model.SharedVariable{}, nil
	}
	return r.withCipherServers(ctx, rows[0].OrgID, rows)
}

func (r *Repo) Get(ctx context.Context, id string) (model.SharedVariable, error) {
	row, err := r.q.GetSharedVariable(ctx, r.tx.Querier(ctx), id)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SharedVariable{}, model.ErrSharedVariableNotFound
	}
	if err != nil {
		return model.SharedVariable{}, err
	}
	out, err := r.withCipherServers(ctx, row.OrgID, []varRow{row})
	if err != nil {
		return model.SharedVariable{}, err
	}
	return out[0], nil
}

func (r *Repo) FindByName(ctx context.Context, orgID string, serverID *string, name string) (model.SharedVariable, bool, error) {
	var (
		row varRow
		err error
	)
	if serverID == nil {
		row, err = r.q.GetOrgSharedVariableByName(ctx, r.tx.Querier(ctx), orgNameParams{OrgID: orgID, Name: name})
	} else {
		row, err = r.q.GetServerSharedVariableByName(ctx, r.tx.Querier(ctx), srvNameParams{ServerID: serverID, Name: name})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return model.SharedVariable{}, false, nil
	}
	if err != nil {
		return model.SharedVariable{}, false, err
	}
	if row.OrgID != orgID {
		return model.SharedVariable{}, false, nil
	}
	out, err := r.withCipherServers(ctx, orgID, []varRow{row})
	if err != nil {
		return model.SharedVariable{}, false, err
	}
	return out[0], true, nil
}

func (r *Repo) Save(ctx context.Context, s model.SharedVariableSave) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		v := s.Variable
		at := v.UpdatedAt
		if at.IsZero() {
			at = time.Now()
		}
		updatedAt := types.DateTime(at.UTC())
		var value *string
		if !v.IsSecret {
			val := v.Value
			value = &val
		}
		if s.Create {
			if err := r.q.InsertSharedVariable(ctx, db, insertParams{
				ID: v.ID, OrgID: v.OrgID, ServerID: v.ServerID, Name: v.Name, IsSecret: v.IsSecret,
				Value: value, Description: v.Description, UpdatedAt: updatedAt, UpdatedBy: v.UpdatedBy,
			}); err != nil {
				return err
			}
		} else if err := r.q.UpdateSharedVariable(ctx, db, updateParams{
			Value: value, Description: v.Description, UpdatedAt: updatedAt, UpdatedBy: v.UpdatedBy, ID: v.ID,
		}); err != nil {
			return err
		}
		if s.ReplaceCiphertexts {
			if err := r.q.DeleteSharedCiphertextsByVariable(ctx, db, v.ID); err != nil {
				return err
			}
		}
		servers := make([]string, 0, len(s.Ciphertexts))
		for id := range s.Ciphertexts {
			servers = append(servers, id)
		}
		sort.Strings(servers)
		for _, serverID := range servers {
			if err := r.q.UpsertSharedCiphertext(ctx, db, upsertCtParams{VariableID: v.ID, ServerID: serverID, Ciphertext: s.Ciphertexts[serverID]}); err != nil {
				return err
			}
		}
		return r.bump(ctx, s.Bump)
	})
}

func (r *Repo) Delete(ctx context.Context, id string, bump []string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteSharedVariable(ctx, r.tx.Querier(ctx), id); err != nil {
			return err
		}
		return r.bump(ctx, bump)
	})
}

func (r *Repo) CiphertextsForServer(ctx context.Context, serverID string) (map[string]string, error) {
	rows, err := r.q.ListSharedCiphertextsForServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.VariableID] = row.Ciphertext
	}
	return out, nil
}

func (r *Repo) DropCiphertexts(ctx context.Context, serverID string, variableIDs []string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		for _, id := range variableIDs {
			if err := r.q.DeleteSharedCiphertext(ctx, r.tx.Querier(ctx), deleteCtParams{VariableID: id, ServerID: serverID}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) PurgeServerCiphertexts(ctx context.Context, serverID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteSharedCiphertextsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		return r.bump(ctx, []string{serverID})
	})
}

func (r *Repo) RaiseVarsRevision(ctx context.Context, serverID string, rev int64) error {
	return r.q.RaiseServerVarsRevision(ctx, r.tx.Querier(ctx), raiseParams{Revision: rev, ServerID: serverID})
}

func (r *Repo) ServerVarsInfo(ctx context.Context, serverID string) (model.ServerVarsInfo, error) {
	row, err := r.q.GetServerVarsInfo(ctx, r.tx.Querier(ctx), serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ServerVarsInfo{}, model.ErrServerNotFound
	}
	if err != nil {
		return model.ServerVarsInfo{}, err
	}
	return model.ServerVarsInfo{ServerID: row.ServerID, OrgID: row.OrganizationID, Name: row.Name, Revision: row.VarsRevision}, nil
}

func (r *Repo) OrgServerKeys(ctx context.Context, orgID string) ([]model.ServerKey, error) {
	servers, err := r.q.ListOrgServers(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	keys, err := r.q.ListOrgPublicKeys(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	byServer := make(map[string]string, len(keys))
	for _, k := range keys {
		byServer[k.ServerID] = k.Value
	}
	out := make([]model.ServerKey, 0, len(servers))
	for _, s := range servers {
		out = append(out, model.ServerKey{ServerID: s.ServerID, Name: s.Name, PublicKey: byServer[s.ServerID]})
	}
	return out, nil
}

// bump raises vars_revision for each server, inside the caller's tx.
func (r *Repo) bump(ctx context.Context, servers []string) error {
	for _, id := range servers {
		if err := r.q.BumpServerVarsRevision(ctx, r.tx.Querier(ctx), id); err != nil {
			return err
		}
	}
	return nil
}

// withCipherServers hydrates rows and fills each one's CipherServers from
// one query over the org's ciphertext index.
func (r *Repo) withCipherServers(ctx context.Context, orgID string, rows []varRow) ([]model.SharedVariable, error) {
	cts, err := r.q.ListCiphertextServersByOrg(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	byVar := map[string][]string{}
	for _, c := range cts {
		byVar[c.VariableID] = append(byVar[c.VariableID], c.ServerID)
	}
	out := make([]model.SharedVariable, 0, len(rows))
	for _, row := range rows {
		v := hydrate(row)
		if servers := byVar[row.ID]; servers != nil {
			v.CipherServers = servers
		}
		out = append(out, v)
	}
	return out, nil
}

func hydrate(row varRow) model.SharedVariable {
	v := model.SharedVariable{
		ID: row.ID, OrgID: row.OrgID, ServerID: row.ServerID, Name: row.Name, IsSecret: row.IsSecret,
		Description: row.Description, UpdatedAt: row.UpdatedAt.Time().UTC(), UpdatedBy: row.UpdatedBy,
		CipherServers: []string{},
	}
	if row.Value != nil {
		v.Value = *row.Value
	}
	return v
}
