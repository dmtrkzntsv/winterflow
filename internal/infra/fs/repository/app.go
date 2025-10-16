package repository

import "winterflow/internal/domain/model"

func NewFsAppRepository() *FsAppRepository {
	return &FsAppRepository{}
}

type FsAppRepository struct {
}

func (r *FsAppRepository) GetApps(serverID string) ([]model.App, error) {
	return make([]model.App, 0), nil
}
