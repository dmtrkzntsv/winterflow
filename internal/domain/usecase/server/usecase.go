package server

import (
	"context"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type UseCase struct {
	srvsvc port.ServerService
	log    *logger.Logger
}

type Deps struct {
	ServerService port.ServerService
	Log           *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		srvsvc: d.ServerService,
		log:    d.Log,
	}
}

func (uc *UseCase) GetServers(ctx context.Context, userID string) ([]model.Server, error) {
	return uc.srvsvc.GetServers(ctx, userID)
}

// ClaimServer claims a pending server registration (by pairing code) into the
// given organization.
func (uc *UseCase) ClaimServer(ctx context.Context, code, organizationID string) (model.Server, error) {
	return uc.srvsvc.ClaimServer(ctx, dto.ClaimServerDTO{
		Code:           code,
		OrganizationID: organizationID,
	})
}
