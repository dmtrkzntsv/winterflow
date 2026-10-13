package template

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return pgsqlgen.New(db).InsertTemplate(ctx, pgsqlgen.InsertTemplateParams(p))
}

func (pgsqlQuerier) BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error) {
	return pgsqlgen.New(db).BumpTemplateRevision(ctx, pgsqlgen.BumpTemplateRevisionParams(p))
}

func (pgsqlQuerier) GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error) {
	row, err := pgsqlgen.New(db).GetTemplate(ctx, pgsqlgen.GetTemplateParams(p))
	return templateRow(row), err
}

func (pgsqlQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error) {
	return pgsqlgen.New(db).GetTemplateIDByName(ctx, pgsqlgen.GetTemplateIDByNameParams(p))
}

func (pgsqlQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
	rows, err := pgsqlgen.New(db).ListTemplates(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]summaryRow, len(rows))
	for i, row := range rows {
		out[i] = summaryRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListTemplateNames(ctx context.Context, db backend.DBTX, orgID string) ([]nameRow, error) {
	rows, err := pgsqlgen.New(db).ListTemplateNames(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]nameRow, len(rows))
	for i, row := range rows {
		out[i] = nameRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error) {
	return pgsqlgen.New(db).DeleteTemplate(ctx, pgsqlgen.DeleteTemplateParams(p))
}

func (pgsqlQuerier) DeleteTemplateRollouts(ctx context.Context, db backend.DBTX, templateID string) error {
	return pgsqlgen.New(db).DeleteTemplateRollouts(ctx, templateID)
}

func (pgsqlQuerier) InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error {
	return pgsqlgen.New(db).InsertTemplateRevision(ctx, pgsqlgen.InsertTemplateRevisionParams(p))
}

func (pgsqlQuerier) GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error) {
	row, err := pgsqlgen.New(db).GetTemplateRevision(ctx, pgsqlgen.GetTemplateRevisionParams(p))
	return revisionRow(row), err
}

func (pgsqlQuerier) ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error) {
	rows, err := pgsqlgen.New(db).ListTemplateRevisions(ctx, templateID)
	if err != nil {
		return nil, err
	}
	out := make([]revisionInfo, len(rows))
	for i, row := range rows {
		out[i] = revisionInfo(row)
	}
	return out, nil
}

func (pgsqlQuerier) ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return pgsqlgen.New(db).ClaimTemplateInstance(ctx, pgsqlgen.ClaimTemplateInstanceParams(p))
}

func (pgsqlQuerier) GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error) {
	row, err := pgsqlgen.New(db).GetLiveTemplateInstance(ctx, pgsqlgen.GetLiveTemplateInstanceParams(p))
	return instanceRow(row), err
}

func (pgsqlQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return instanceRows(pgsqlgen.New(db).ListLiveTemplateInstances(ctx, orgID))
}

func (pgsqlQuerier) ListUpgradableInstances(ctx context.Context, db backend.DBTX, p upgradeParams) ([]instanceRow, error) {
	return instanceRows(pgsqlgen.New(db).ListUpgradableInstances(ctx, pgsqlgen.ListUpgradableInstancesParams(p)))
}

func instanceRows(rows []pgsqlgen.TemplateInstance, err error) ([]instanceRow, error) {
	if err != nil {
		return nil, err
	}
	out := make([]instanceRow, len(rows))
	for i, row := range rows {
		out[i] = instanceRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error {
	return pgsqlgen.New(db).MarkTemplateInstanceSaved(ctx, pgsqlgen.MarkTemplateInstanceSavedParams(p))
}

func (pgsqlQuerier) SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) error {
	return pgsqlgen.New(db).SetTemplateInstanceState(ctx, pgsqlgen.SetTemplateInstanceStateParams(p))
}

func (pgsqlQuerier) SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error {
	return pgsqlgen.New(db).SetTemplateInstanceError(ctx, pgsqlgen.SetTemplateInstanceErrorParams(p))
}

func (pgsqlQuerier) GetTemplateInstanceByID(ctx context.Context, db backend.DBTX, id string) (instanceRow, error) {
	row, err := pgsqlgen.New(db).GetTemplateInstanceByID(ctx, id)
	return instanceRow(row), err
}

func (pgsqlQuerier) GetLiveTemplateInstanceForApp(ctx context.Context, db backend.DBTX, p forAppParams) (string, error) {
	return pgsqlgen.New(db).GetLiveTemplateInstanceForApp(ctx, pgsqlgen.GetLiveTemplateInstanceForAppParams(p))
}

func (pgsqlQuerier) RetireTemplateInstancesForApp(ctx context.Context, db backend.DBTX, p retireParams) error {
	return pgsqlgen.New(db).RetireTemplateInstancesForApp(ctx, pgsqlgen.RetireTemplateInstancesForAppParams(p))
}
