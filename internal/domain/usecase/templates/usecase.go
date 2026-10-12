// Package templates is the API side of templates and their instances:
// template revisions, and instances rendered into ordinary app.save
// payloads and dispatched through the app usecase (so every save-app
// check applies). The agent never sees a template.
package templates

import (
	"context"
	"time"

	"github.com/google/uuid"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

// SharedNames lists an org's shared variables (the save warnings skip
// their names); port.SharedVariableRepository satisfies it.
type SharedNames interface {
	ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error)
}

// AppStatuses is the live status cache (*status.Cache satisfies it).
type AppStatuses interface {
	AppStatuses(serverID string, now time.Time) []command.AppStatus
}

// Deps wires the usecase. Shared and Status may be nil.
type Deps struct {
	Repo   port.TemplateRepository
	Apps   *appuc.UseCase
	Shared SharedNames
	Status AppStatuses
	Log    *logger.Logger
}

// UseCase serves the template and instance routes.
type UseCase struct {
	repo   port.TemplateRepository
	apps   *appuc.UseCase
	shared SharedNames
	status AppStatuses
	log    *logger.Logger
}

// New builds the usecase.
func New(d Deps) *UseCase {
	return &UseCase{repo: d.Repo, apps: d.Apps, shared: d.Shared, status: d.Status, log: d.Log}
}

// ValidID reports whether id is a canonical UUID (template ids are; on
// Postgres anything else could not even be looked up).
func ValidID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}
