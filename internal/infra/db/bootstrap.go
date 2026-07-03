package db

import (
	"winterflow/internal/infra/db/models"

	"github.com/uptrace/bun"
)

func registerModels(db *bun.DB) {
	db.RegisterModel((*models.Organization)(nil))
	db.RegisterModel((*models.OrganizationUser)(nil))
	db.RegisterModel((*models.User)(nil))
	db.RegisterModel((*models.UserToken)(nil))
	db.RegisterModel((*models.UserConnectedAccount)(nil))
	db.RegisterModel((*models.Server)(nil))
	db.RegisterModel((*models.ServerCapability)(nil))
	db.RegisterModel((*models.ServerFeature)(nil))
	db.RegisterModel((*models.ServerRegistration)(nil))
	db.RegisterModel((*models.ServerCertificate)(nil))
	db.RegisterModel((*models.App)(nil))
}
