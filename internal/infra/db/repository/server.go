package repository

import (
	"context"
	"encoding/base64"
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

func (r *DbServerRepository) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	return make([]model.Server, 0), nil
}

func (r *DbServerRepository) AddServer(ctx context.Context, dto dto.ServerDTO) (model.Server, error) {
	return model.Server{}, nil
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
