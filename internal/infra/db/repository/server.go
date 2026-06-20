package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"

	"github.com/uptrace/bun"
)

func NewDbServerRepository(db *db.BunConnection, log *logger.Logger) *DbServerRepository {
	return &DbServerRepository{
		db:  db,
		log: log,
	}
}

type DbServerRepository struct {
	db  *db.BunConnection
	log *logger.Logger
}

// GetServers returns the servers owned by any organization the user belongs to.
func (r *DbServerRepository) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	dbi := r.db.GetDB()
	var servers []models.Server
	err := dbi.NewSelect().
		Model(&servers).
		Where("organization_id IN (?)",
			dbi.NewSelect().
				Model((*models.OrganizationUser)(nil)).
				Column("organization_id").
				Where("user_id = ?", userID),
		).
		Order("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]model.Server, 0, len(servers))
	for i := range servers {
		out = append(out, toDomainServer(&servers[i]))
	}
	return out, nil
}

func (r *DbServerRepository) AddServer(ctx context.Context, dto dto.ServerDTO) (model.Server, error) {
	return model.Server{}, nil
}

// toDomainServer maps a DB server row to the domain model.
func toDomainServer(s *models.Server) model.Server {
	srv := model.Server{
		ID:             s.ServerID,
		OrganizationID: s.OrganizationID,
		Name:           s.Name,
		CreatedAt:      s.CreatedAt.Time(),
	}
	if s.LastSeen != nil {
		t := s.LastSeen.Time()
		srv.LastSeenAt = &t
	}
	return srv
}

func (r *DbServerRepository) RegisterServer(ctx context.Context, dto dto.ServerRegistrationDTO) error {
	reg := &models.ServerRegistration{
		BaseModel:            bun.BaseModel{},
		ServerID:             dto.ServerID,
		CertificateID:        dto.CertificateID,
		Hostname:             dto.Hostname,
		Code:                 dto.Code,
		ExpiresAt:            types.DateTime(dto.ExpiresAt),
		Certificate:          base64.StdEncoding.EncodeToString([]byte(dto.Certificate)),
		CertificateExpiresAt: types.DateTime(dto.CertificateExpiresAt),
		CreatedAt:            util.NewDateTime(),
	}

	dbi := r.db.GetDB()

	_, err := dbi.NewDelete().Model(&models.ServerRegistration{}).Where("server_id = ?", dto.ServerID).Exec(ctx)
	if err != nil {
		return err
	}

	_, err = dbi.NewInsert().Model(reg).Exec(ctx)
	if err != nil {
		return err
	}

	return nil
}

// IsServerRegistered reports whether a materialized Server row exists.
func (r *DbServerRepository) IsServerRegistered(ctx context.Context, serverID string) (bool, error) {
	return r.db.GetDB().NewSelect().
		Model((*models.Server)(nil)).
		Where("server_id = ?", serverID).
		Exists(ctx)
}

// HasAnyServer reports whether any Server has been claimed. Used by standalone
// bootstrap to decide whether the embedded agent still needs a pending
// registration.
func (r *DbServerRepository) HasAnyServer(ctx context.Context) (bool, error) {
	return r.db.GetDB().NewSelect().Model((*models.Server)(nil)).Exists(ctx)
}

// ClearPendingRegistrations removes all unclaimed registrations. Standalone
// re-issues a single fresh one each boot (while unclaimed), so stale/expired
// rows from earlier boots don't accumulate or block the claim.
func (r *DbServerRepository) ClearPendingRegistrations(ctx context.Context) error {
	_, err := r.db.GetDB().NewDelete().
		Model((*models.ServerRegistration)(nil)).
		Where("1 = 1").
		Exec(ctx)
	return err
}

// ClaimServer consumes a pending registration by code and materializes the
// Server + its active certificate, owned by the given organization, in one
// transaction. Mirrors the v1 RegisterAgent flow.
func (r *DbServerRepository) ClaimServer(ctx context.Context, d dto.ClaimServerDTO) (model.Server, error) {
	dbi := r.db.GetDB()

	var reg models.ServerRegistration
	err := dbi.NewSelect().
		Model(&reg).
		Where("code = ?", d.Code).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Server{}, model.ErrInvalidRegistrationCode
		}
		return model.Server{}, err
	}
	if reg.ExpiresAt.Time().Before(time.Now()) {
		return model.Server{}, model.ErrRegistrationCodeExpired
	}

	server := &models.Server{
		ServerID:           reg.ServerID,
		OrganizationID:     d.OrganizationID,
		Name:               reg.Hostname,
		SubscriptionStatus: model.SubscriptionStatusUnpaid.Value(),
		CreatedAt:          util.NewDateTime(),
	}
	cert := &models.ServerCertificate{
		CertificateID: reg.CertificateID,
		ServerID:      reg.ServerID,
		Certificate:   reg.Certificate,
		IsActive:      true,
		ExpiresAt:     reg.CertificateExpiresAt,
		CreatedAt:     util.NewDateTime(),
	}

	err = r.db.Transaction(ctx, func(tx bun.IDB) error {
		if _, err := tx.NewInsert().Model(server).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(cert).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().
			Model((*models.ServerRegistration)(nil)).
			Where("server_id = ?", reg.ServerID).
			Exec(ctx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.Server{}, err
	}

	return toDomainServer(server), nil
}

// PendingRegistrationCode returns the code of the most recent unclaimed
// registration (used by standalone auto-claim, where the box has one logical
// embedded agent). Returns ok=false only when there are no pending
// registrations. The newest wins so leftover registrations from earlier boots
// don't block the claim.
func (r *DbServerRepository) PendingRegistrationCode(ctx context.Context) (string, bool, error) {
	var reg models.ServerRegistration
	err := r.db.GetDB().NewSelect().
		Model(&reg).
		Order("created_at DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return reg.Code, true, nil
}
