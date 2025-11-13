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
	db.RegisterModel((*models.Agent)(nil))
	db.RegisterModel((*models.AgentCapability)(nil))
	db.RegisterModel((*models.AgentFeature)(nil))
	db.RegisterModel((*models.AgentRegistration)(nil))
	db.RegisterModel((*models.AgentCertificate)(nil))
	db.RegisterModel((*models.ReleaseVersion)(nil))
	db.RegisterModel((*models.App)(nil))
}
