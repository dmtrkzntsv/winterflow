package server

import "winterflow/internal/domain/port"

type UseCase struct {
	repository port.ServerRepository
}

type Deps struct {
	ServerRepo port.ServerRepository
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		repository: d.ServerRepo,
	}
}
