package server

import (
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

type UseCase struct {
	repo port.AppRepository
}

type Deps struct {
	AppRepo port.AppRepository
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		repo: d.AppRepo,
	}
}

func (uc *UseCase) GetApps(serverID string) ([]model.App, error) {
	return uc.repo.GetApps(serverID)
}
