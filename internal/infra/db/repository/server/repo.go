// Package server implements port.ServerRepository: claimed servers, their
// capabilities and features, pending registrations and certificates.
package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

type (
	serverRow          = sqlitegen.Server
	capabilityRow      = sqlitegen.ListCapabilitiesByUserRow
	featureRow         = sqlitegen.ServerFeature
	registrationRow    = sqlitegen.ServerRegistration
	ownsParams         = sqlitegen.UserOwnsServerParams
	inOrgParams        = sqlitegen.ServerInOrganizationParams
	insertServerParams = sqlitegen.InsertServerParams
	touchParams        = sqlitegen.TouchServerLastSeenParams
	capabilityParams   = sqlitegen.UpsertCapabilityParams
	featureParams      = sqlitegen.UpsertFeatureParams
	getCapParams       = sqlitegen.GetCapabilityParams
	registrationParams = sqlitegen.InsertRegistrationParams
	certificateParams  = sqlitegen.InsertCertificateParams
)

type querier interface {
	ListServersByUser(ctx context.Context, db backend.DBTX, userID string) ([]serverRow, error)
	ListCapabilitiesByUser(ctx context.Context, db backend.DBTX, userID string) ([]capabilityRow, error)
	ListFeaturesByUser(ctx context.Context, db backend.DBTX, userID string) ([]featureRow, error)
	ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	ListOrgUserIDs(ctx context.Context, db backend.DBTX, orgID string) ([]string, error)
	UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error)
	ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error)
	HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error)
	GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error)
	InsertServer(ctx context.Context, db backend.DBTX, p insertServerParams) error
	TouchServerLastSeen(ctx context.Context, db backend.DBTX, p touchParams) error
	UpsertCapability(ctx context.Context, db backend.DBTX, p capabilityParams) error
	UpsertFeature(ctx context.Context, db backend.DBTX, p featureParams) error
	GetCapability(ctx context.Context, db backend.DBTX, p getCapParams) (string, error)
	InsertRegistration(ctx context.Context, db backend.DBTX, p registrationParams) error
	DeleteRegistration(ctx context.Context, db backend.DBTX, serverID string) error
	DeleteAllRegistrations(ctx context.Context, db backend.DBTX) error
	GetRegistrationByCode(ctx context.Context, db backend.DBTX, code string) (registrationRow, error)
	GetLatestRegistrationCode(ctx context.Context, db backend.DBTX) (string, error)
	InsertCertificate(ctx context.Context, db backend.DBTX, p certificateParams) error
}

// Repo is the servers repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.ServerRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("server repository: unknown database driver %q", driver)
}

// GetServers returns the servers owned by any organization the user belongs
// to, oldest first, with their capabilities (by name) and features.
func (r *Repo) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	db := r.tx.Querier(ctx)
	rows, err := r.q.ListServersByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	caps, err := r.q.ListCapabilitiesByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	feats, err := r.q.ListFeaturesByUser(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	capsBy := map[string][]model.Capability{}
	for _, c := range caps {
		capsBy[c.ServerID] = append(capsBy[c.ServerID], model.Capability{Name: c.Name, Value: c.Value})
	}
	featsBy := map[string]map[string]bool{}
	for _, f := range feats {
		if featsBy[f.ServerID] == nil {
			featsBy[f.ServerID] = map[string]bool{}
		}
		featsBy[f.ServerID][f.Name] = f.IsEnabled
	}
	out := make([]model.Server, 0, len(rows))
	for _, row := range rows {
		s := hydrate(row)
		// Both stay nil when the server has none (omitted from JSON).
		s.Capabilities = capsBy[row.ServerID]
		s.Features = featsBy[row.ServerID]
		out = append(out, s)
	}
	return out, nil
}

func hydrate(row serverRow) model.Server {
	s := model.Server{
		ID:             row.ServerID,
		OrganizationID: row.OrganizationID,
		Name:           row.Name,
		CreatedAt:      row.CreatedAt.Time(),
	}
	if row.LastSeen != nil {
		t := row.LastSeen.Time()
		s.LastSeenAt = &t
	}
	return s
}

// RegisterServer replaces the server's pending registration atomically: the
// old row is deleted and the new one inserted in one transaction, so a failed
// insert keeps the previous registration. The PEM is stored base64-encoded.
func (r *Repo) RegisterServer(ctx context.Context, d dto.ServerRegistrationDTO) error {
	now := types.NewDateTime()
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.DeleteRegistration(ctx, db, d.ServerID); err != nil {
			return err
		}
		return r.q.InsertRegistration(ctx, db, registrationParams{
			ServerID:             d.ServerID,
			CertificateID:        d.CertificateID,
			Hostname:             d.Hostname,
			Code:                 d.Code,
			ExpiresAt:            types.DateTime(d.ExpiresAt),
			Certificate:          base64.StdEncoding.EncodeToString(d.Certificate),
			CertificateExpiresAt: types.DateTime(d.CertificateExpiresAt),
			CreatedAt:            now,
		})
	})
}

// GetOrgUserIDs returns the user ids of every member of the organization
// (ids only: no credentials are read).
func (r *Repo) GetOrgUserIDs(ctx context.Context, orgID string) ([]string, error) {
	return r.q.ListOrgUserIDs(ctx, r.tx.Querier(ctx), orgID)
}

// GetServerUserIDs returns the user ids of every member of the organization
// that owns the server. Unknown server: empty, no error.
func (r *Repo) GetServerUserIDs(ctx context.Context, serverID string) ([]string, error) {
	return r.q.ListServerUserIDs(ctx, r.tx.Querier(ctx), serverID)
}

// UserOwnsServer reports whether the server belongs to one of the user's
// organizations. Unknown server or non-member: false, no error.
func (r *Repo) UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error) {
	return r.q.UserOwnsServer(ctx, r.tx.Querier(ctx), ownsParams{ServerID: serverID, UserID: userID})
}

// ServerInOrg reports whether the server belongs to orgID. Unknown server:
// false, no error.
func (r *Repo) ServerInOrg(ctx context.Context, serverID, orgID string) (bool, error) {
	return r.q.ServerInOrganization(ctx, r.tx.Querier(ctx), inOrgParams{ServerID: serverID, OrganizationID: orgID})
}

// HasAnyServer reports whether any server has been claimed.
func (r *Repo) HasAnyServer(ctx context.Context) (bool, error) {
	return r.q.HasAnyServer(ctx, r.tx.Querier(ctx))
}

// FirstServerID returns the id of the (single, in standalone) claimed
// server, if any.
func (r *Repo) FirstServerID(ctx context.Context) (string, bool, error) {
	id, err := r.q.GetFirstServerID(ctx, r.tx.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return id, true, nil
}

// TouchLastSeen records that a server reported in just now. No-op for an
// unclaimed server.
func (r *Repo) TouchLastSeen(ctx context.Context, serverID string) error {
	now := types.NewDateTime()
	return r.q.TouchServerLastSeen(ctx, r.tx.Querier(ctx), touchParams{LastSeen: &now, ServerID: serverID})
}

// SaveCapabilities upserts a server's capabilities and features in one
// transaction.
func (r *Repo) SaveCapabilities(ctx context.Context, serverID string, capabilities map[string]string, features map[string]bool) error {
	now := types.NewDateTime()
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		for name, value := range capabilities {
			if err := r.q.UpsertCapability(ctx, db, capabilityParams{ServerID: serverID, Name: name, Value: value, UpdatedAt: now}); err != nil {
				return err
			}
		}
		for name, enabled := range features {
			if err := r.q.UpsertFeature(ctx, db, featureParams{ServerID: serverID, Name: name, IsEnabled: enabled}); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetCapability returns one capability value; ok is false when the server
// has none recorded under name.
func (r *Repo) GetCapability(ctx context.Context, serverID, name string) (string, bool, error) {
	v, err := r.q.GetCapability(ctx, r.tx.Querier(ctx), getCapParams{ServerID: serverID, Name: name})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// ClearPendingRegistrations removes all unclaimed registrations.
func (r *Repo) ClearPendingRegistrations(ctx context.Context) error {
	return r.q.DeleteAllRegistrations(ctx, r.tx.Querier(ctx))
}

// ClaimServer consumes a pending registration by code and materializes the
// server and its active certificate, owned by the organization, in one
// transaction. The returned server has no capabilities yet, and its
// CreatedAt is the unrounded local time.Now() it was stored with.
func (r *Repo) ClaimServer(ctx context.Context, d dto.ClaimServerDTO) (model.Server, error) {
	reg, err := r.q.GetRegistrationByCode(ctx, r.tx.Querier(ctx), d.Code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Server{}, model.ErrInvalidRegistrationCode
		}
		return model.Server{}, err
	}
	if reg.ExpiresAt.Time().Before(time.Now()) {
		return model.Server{}, model.ErrRegistrationCodeExpired
	}
	created := types.NewDateTime()
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		if err := r.q.InsertServer(ctx, db, insertServerParams{
			ServerID:       reg.ServerID,
			OrganizationID: d.OrganizationID,
			Name:           reg.Hostname,
			CreatedAt:      created,
		}); err != nil {
			return err
		}
		if err := r.q.InsertCertificate(ctx, db, certificateParams{
			CertificateID: reg.CertificateID,
			ServerID:      reg.ServerID,
			Certificate:   reg.Certificate,
			IsActive:      true,
			ExpiresAt:     reg.CertificateExpiresAt,
			CreatedAt:     types.NewDateTime(),
		}); err != nil {
			return err
		}
		return r.q.DeleteRegistration(ctx, db, reg.ServerID)
	})
	if err != nil {
		return model.Server{}, err
	}
	return model.Server{
		ID:             reg.ServerID,
		OrganizationID: d.OrganizationID,
		Name:           reg.Hostname,
		CreatedAt:      created.Time(),
	}, nil
}

// PendingRegistrationCode returns the code of the newest unclaimed
// registration; ok is false when there is none.
func (r *Repo) PendingRegistrationCode(ctx context.Context) (string, bool, error) {
	code, err := r.q.GetLatestRegistrationCode(ctx, r.tx.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return code, true, nil
}
