// Package template implements port.TemplateRepository over the templates,
// template_revisions and template_instances tables.
package template

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	templateRow    = sqlitegen.Template
	summaryRow     = sqlitegen.ListTemplatesRow
	revisionRow    = sqlitegen.TemplateRevision
	revisionInfo   = sqlitegen.ListTemplateRevisionsRow
	instanceRow    = sqlitegen.TemplateInstance
	insertParams   = sqlitegen.InsertTemplateParams
	bumpParams     = sqlitegen.BumpTemplateRevisionParams
	getParams      = sqlitegen.GetTemplateParams
	byNameParams   = sqlitegen.GetTemplateIDByNameParams
	deleteParams   = sqlitegen.DeleteTemplateParams
	insertRevision = sqlitegen.InsertTemplateRevisionParams
	getRevision    = sqlitegen.GetTemplateRevisionParams
	claimParams    = sqlitegen.ClaimTemplateInstanceParams
	liveParams     = sqlitegen.GetLiveTemplateInstanceParams
	savedParams    = sqlitegen.MarkTemplateInstanceSavedParams
	stateParams    = sqlitegen.SetTemplateInstanceStateParams
	errorParams    = sqlitegen.SetTemplateInstanceErrorParams
)

type querier interface {
	InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error)
	BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error)
	GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error)
	GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error)
	ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error)
	DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error)
	InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error
	GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error)
	ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error)
	ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error)
	ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error)
	MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) (int64, error)
	SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) (int64, error)
	SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error
}

// Repo is the templates repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.TemplateRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("template repository: unknown database driver %q", driver)
}

// CreateTemplate inserts the template (latest_revision 1) and revision 1.
func (r *Repo) CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error {
	now := types.NewDateTime()
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.InsertTemplate(ctx, r.tx.Querier(ctx), insertParams{
			ID: t.ID, OrgID: t.OrgID, Name: t.Name, CreatedAt: now, CreatedBy: t.CreatedBy, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrTemplateNameTaken
		}
		rev.TemplateID, rev.Revision = t.ID, 1
		return r.insertRevision(ctx, rev, now)
	})
}

// AddRevision bumps latest_revision from base (renaming the template) and
// inserts the new revision, in one transaction.
func (r *Repo) AddRevision(ctx context.Context, orgID, templateID, name string, base int64, rev model.TemplateRevision) (int64, error) {
	now := types.NewDateTime()
	next := base + 1
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		cur, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrTemplateNotFound
		}
		if err != nil {
			return err
		}
		if cur.Name != name {
			switch other, err := r.q.GetTemplateIDByName(ctx, r.tx.Querier(ctx), byNameParams{OrgID: orgID, Name: name}); {
			case err == nil && other != templateID:
				return model.ErrTemplateNameTaken
			case err != nil && !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		n, err := r.q.BumpTemplateRevision(ctx, r.tx.Querier(ctx), bumpParams{
			Name: name, UpdatedAt: now, ID: templateID, OrgID: orgID, LatestRevision: base,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrRevisionConflict
		}
		rev.TemplateID, rev.Revision = templateID, next
		return r.insertRevision(ctx, rev, now)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

func (r *Repo) insertRevision(ctx context.Context, rev model.TemplateRevision, now types.DateTime) error {
	return r.q.InsertTemplateRevision(ctx, r.tx.Querier(ctx), insertRevision{
		TemplateID: rev.TemplateID, Revision: rev.Revision, Body: string(rev.Body), Note: rev.Note,
		CreatedAt: now, CreatedBy: rev.CreatedBy,
	})
}

// GetTemplate returns the org's template.
func (r *Repo) GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error) {
	row, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
	if errors.Is(err, sql.ErrNoRows) {
		return model.Template{}, model.ErrTemplateNotFound
	}
	if err != nil {
		return model.Template{}, err
	}
	return hydrateTemplate(row), nil
}

// ListTemplates returns the org's templates by name with instance counts.
func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error) {
	rows, err := r.q.ListTemplates(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TemplateSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TemplateSummary{
			Template: hydrateTemplate(templateRow{
				ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
				CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
			}),
			Instances: row.Instances,
			Behind:    row.Behind,
		})
	}
	return out, nil
}

// GetRevision returns one revision with its body.
func (r *Repo) GetRevision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, error) {
	row, err := r.q.GetTemplateRevision(ctx, r.tx.Querier(ctx), getRevision{TemplateID: templateID, Revision: revision})
	if errors.Is(err, sql.ErrNoRows) {
		return model.TemplateRevision{}, model.ErrTemplateRevisionGone
	}
	if err != nil {
		return model.TemplateRevision{}, err
	}
	return model.TemplateRevision{
		TemplateID: row.TemplateID, Revision: row.Revision, Body: []byte(row.Body), Note: row.Note,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
	}, nil
}

// ListRevisions returns the revisions newest first, without bodies.
func (r *Repo) ListRevisions(ctx context.Context, templateID string) ([]model.TemplateRevision, error) {
	rows, err := r.q.ListTemplateRevisions(ctx, r.tx.Querier(ctx), templateID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TemplateRevision, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TemplateRevision{
			TemplateID: templateID, Revision: row.Revision, Note: row.Note,
			CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
		})
	}
	return out, nil
}

// DeleteTemplate deletes the template unless an instance is live: one
// guarded statement, then a read to tell "in use" from "unknown".
func (r *Repo) DeleteTemplate(ctx context.Context, orgID, templateID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.DeleteTemplate(ctx, r.tx.Querier(ctx), deleteParams{ID: templateID, OrgID: orgID})
		if err != nil || n > 0 {
			return err
		}
		_, err = r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrTemplateNotFound
		}
		if err != nil {
			return err
		}
		return model.ErrTemplateInUse
	})
}

// ClaimInstance inserts inst as pending, or returns the live instance that
// holds its key (the partial unique index makes the insert a no-op).
func (r *Repo) ClaimInstance(ctx context.Context, inst model.Instance) (model.Instance, bool, error) {
	params, names, err := encodeValues(inst.Params, inst.SecretNames)
	if err != nil {
		return model.Instance{}, false, err
	}
	now := types.NewDateTime()
	var (
		out      model.Instance
		inserted bool
	)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		p := claimParams{
			ID: inst.ID, OrgID: inst.OrgID, InstanceKey: inst.Key, AppID: inst.AppID, ServerID: inst.ServerID,
			Revision: inst.Revision, Params: params, SecretNames: names, RequestHash: inst.RequestHash,
			CreatedAt: now, UpdatedAt: now,
		}
		if inst.TemplateID != "" {
			id := inst.TemplateID
			p.TemplateID = &id
		}
		n, err := r.q.ClaimTemplateInstance(ctx, r.tx.Querier(ctx), p)
		if err != nil {
			return err
		}
		inserted = n > 0
		row, err := r.q.GetLiveTemplateInstance(ctx, r.tx.Querier(ctx), liveParams{OrgID: inst.OrgID, InstanceKey: inst.Key})
		if err != nil {
			return err
		}
		out, err = hydrateInstance(row)
		return err
	})
	return out, inserted, err
}

// GetInstance returns the org's live instance with key.
func (r *Repo) GetInstance(ctx context.Context, orgID, key string) (model.Instance, error) {
	row, err := r.q.GetLiveTemplateInstance(ctx, r.tx.Querier(ctx), liveParams{OrgID: orgID, InstanceKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return model.Instance{}, model.ErrInstanceNotFound
	}
	if err != nil {
		return model.Instance{}, err
	}
	return hydrateInstance(row)
}

// ListInstances returns the org's pending and created instances.
func (r *Repo) ListInstances(ctx context.Context, orgID string) ([]model.Instance, error) {
	rows, err := r.q.ListLiveTemplateInstances(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Instance, 0, len(rows))
	for _, row := range rows {
		inst, err := hydrateInstance(row)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// MarkSaved records a confirmed save.
func (r *Repo) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error {
	p, names, err := encodeValues(params, secretNames)
	if err != nil {
		return err
	}
	_, err = r.q.MarkTemplateInstanceSaved(ctx, r.tx.Querier(ctx), savedParams{
		Revision: revision, Params: p, SecretNames: names, UpdatedAt: types.NewDateTime(), ID: id,
	})
	return err
}

// SetState moves a live instance to state.
func (r *Repo) SetState(ctx context.Context, id string, state model.InstanceState) error {
	_, err := r.q.SetTemplateInstanceState(ctx, r.tx.Querier(ctx), stateParams{
		State: string(state), UpdatedAt: types.NewDateTime(), ID: id,
	})
	return err
}

// SetError records the last failed command's error.
func (r *Repo) SetError(ctx context.Context, id, msg string) error {
	return r.q.SetTemplateInstanceError(ctx, r.tx.Querier(ctx), errorParams{
		LastError: msg, UpdatedAt: types.NewDateTime(), ID: id,
	})
}

// encodeValues serializes the plain values (an object; nil is {}) and the
// secret names (sorted; nil is []).
func encodeValues(params map[string]string, secretNames []string) (string, string, error) {
	if params == nil {
		params = map[string]string{}
	}
	p, err := json.Marshal(params)
	if err != nil {
		return "", "", err
	}
	names := append([]string{}, secretNames...)
	sort.Strings(names)
	n, err := json.Marshal(names)
	if err != nil {
		return "", "", err
	}
	return string(p), string(n), nil
}

func hydrateTemplate(row templateRow) model.Template {
	return model.Template{
		ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt.Time(),
	}
}

func hydrateInstance(row instanceRow) (model.Instance, error) {
	inst := model.Instance{
		ID: row.ID, OrgID: row.OrgID, Key: row.InstanceKey, AppID: row.AppID, ServerID: row.ServerID,
		Revision: row.Revision, RequestHash: row.RequestHash, State: model.InstanceState(row.State),
		LastError: row.LastError, CreatedAt: row.CreatedAt.Time(), UpdatedAt: row.UpdatedAt.Time(),
	}
	if row.TemplateID != nil {
		inst.TemplateID = *row.TemplateID
	}
	if err := json.Unmarshal([]byte(row.Params), &inst.Params); err != nil {
		return model.Instance{}, fmt.Errorf("instance %s: params: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.SecretNames), &inst.SecretNames); err != nil {
		return model.Instance{}, fmt.Errorf("instance %s: secret_names: %w", row.ID, err)
	}
	return inst, nil
}
