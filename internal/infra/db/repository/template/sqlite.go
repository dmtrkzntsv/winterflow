package template

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return sqlitegen.New(db).InsertTemplate(ctx, p)
}

func (sqliteQuerier) BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error) {
	return sqlitegen.New(db).BumpTemplateRevision(ctx, p)
}

func (sqliteQuerier) GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error) {
	return sqlitegen.New(db).GetTemplate(ctx, p)
}

func (sqliteQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error) {
	return sqlitegen.New(db).GetTemplateIDByName(ctx, p)
}

func (sqliteQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
	return sqlitegen.New(db).ListTemplates(ctx, orgID)
}

func (sqliteQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID string) ([]nameRow, error) {
	return sqlitegen.New(db).ListTemplateNames(ctx, orgID)
}

func (sqliteQuerier) DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error) {
	return sqlitegen.New(db).DeleteTemplate(ctx, p)
}

func (sqliteQuerier) DeleteTemplateRollouts(ctx context.Context, db backend.DBTX, templateID string) error {
	return sqlitegen.New(db).DeleteTemplateRollouts(ctx, templateID)
}

func (sqliteQuerier) InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error {
	return sqlitegen.New(db).InsertTemplateRevision(ctx, p)
}

func (sqliteQuerier) GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error) {
	return sqlitegen.New(db).GetTemplateRevision(ctx, p)
}

func (sqliteQuerier) ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error) {
	return sqlitegen.New(db).ListTemplateRevisions(ctx, templateID)
}

func (sqliteQuerier) ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimTemplateInstance(ctx, p)
}

func (sqliteQuerier) GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error) {
	return sqlitegen.New(db).GetLiveTemplateInstance(ctx, p)
}

func (sqliteQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return sqlitegen.New(db).ListLiveTemplateInstances(ctx, orgID)
}

func (sqliteQuerier) ListUpgradableInstances(ctx context.Context, db backend.DBTX, p upgradeParams) ([]instanceRow, error) {
	return sqlitegen.New(db).ListUpgradableInstances(ctx, p)
}

func (sqliteQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error {
	return sqlitegen.New(db).MarkTemplateInstanceSaved(ctx, p)
}

func (sqliteQuerier) SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) error {
	return sqlitegen.New(db).SetTemplateInstanceState(ctx, p)
}

func (sqliteQuerier) SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error {
	return sqlitegen.New(db).SetTemplateInstanceError(ctx, p)
}

func (sqliteQuerier) GetTemplateInstanceByID(ctx context.Context, db backend.DBTX, id string) (instanceRow, error) {
	return sqlitegen.New(db).GetTemplateInstanceByID(ctx, id)
}

func (sqliteQuerier) GetLiveTemplateInstanceForApp(ctx context.Context, db backend.DBTX, p forAppParams) (string, error) {
	return sqlitegen.New(db).GetLiveTemplateInstanceForApp(ctx, p)
}

func (sqliteQuerier) RetireTemplateInstancesForApp(ctx context.Context, db backend.DBTX, p retireParams) error {
	return sqlitegen.New(db).RetireTemplateInstancesForApp(ctx, p)
}
