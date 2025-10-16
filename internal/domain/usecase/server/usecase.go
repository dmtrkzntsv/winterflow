package server

import "winterflow/internal/domain/port"

type UseCase struct {
	repository port.ServerRepository
}

type Deps struct {
	Repository port.ServerRepository
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		repository: d.Repository,
	}
}
