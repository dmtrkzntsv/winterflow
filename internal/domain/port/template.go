package port

import (
	"context"

	"winterflow/internal/domain/model"
)

// TemplateRepository persists templates, their revisions and instances.
// Every template read is scoped to an org, which reads its own templates
// and every catalog template: another org's id is
// model.ErrTemplateNotFound. Only an org's own templates change through
// AddRevision and DeleteTemplate.
type TemplateRepository interface {
	InstanceLinks
	// CreateTemplate inserts the template (t.Source says whose: an org
	// template needs OrgID, a catalog one Slug) and its revision 1 in one
	// transaction; model.ErrTemplateNameTaken when the org has the name or
	// the catalog the slug.
	CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error
	// AddRevision adds revision base+1 of the org's template (renaming it
	// to name) in one transaction and returns it: model.ErrRevisionConflict
	// when the latest revision is not base, model.ErrTemplateNameTaken when
	// another template of the org has name, model.ErrTemplateNotFound when
	// the org has no such template (a catalog one included).
	AddRevision(ctx context.Context, orgID, templateID, name string, base int64, rev model.TemplateRevision) (int64, error)
	// GetTemplate returns a template the org may read.
	GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error)
	// ListTemplates returns the org's templates, then the catalog's (a
	// withdrawn one only while the org has live instances of it), with the
	// org's instance counts.
	ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error)
	// ListTemplateNames returns the names of the templates the org may
	// read, by id.
	ListTemplateNames(ctx context.Context, orgID string) (map[string]string, error)
	// GetRevision returns one revision; model.ErrTemplateRevisionGone when
	// the template has no such revision.
	GetRevision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, error)
	// ListRevisions returns the revisions newest first, without bodies.
	ListRevisions(ctx context.Context, templateID string) ([]model.TemplateRevision, error)
	// DeleteTemplate removes the org's template, its revisions and the
	// org's upgrade rollouts of it: model.ErrTemplateInUse while an
	// instance is pending or created, model.ErrTemplateNotFound when the org
	// has no such template (a catalog one included).
	DeleteTemplate(ctx context.Context, orgID, templateID string) error

	// ClaimInstance inserts inst as pending unless the org already has a
	// live (not unlinked) instance with its key; it returns the stored
	// instance and whether this call inserted it.
	ClaimInstance(ctx context.Context, inst model.Instance) (model.Instance, bool, error)
	// GetInstance returns the org's live instance with key (pending,
	// created or deleted); model.ErrInstanceNotFound when none.
	GetInstance(ctx context.Context, orgID, key string) (model.Instance, error)
	// GetInstanceByID returns an instance in any state (an unlinked one
	// too); model.ErrInstanceNotFound when none.
	GetInstanceByID(ctx context.Context, id string) (model.Instance, error)
	// ListInstances returns the org's pending and created instances,
	// oldest first.
	ListInstances(ctx context.Context, orgID string) ([]model.Instance, error)
	// ListUpgradableInstances returns the org's created instances of
	// templateID below revision, oldest first, without an instance whose
	// server is not the org's: what an upgrade rollout of the org may touch.
	ListUpgradableInstances(ctx context.Context, orgID, templateID string, revision int64) ([]model.Instance, error)
	// MarkSaved records a save the agent confirmed: revision, values,
	// hosts and state created, last error cleared. A deleted or unlinked
	// instance is left alone.
	MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string, hosts []model.InstanceHost) error
	// SetBackups replaces a pending instance's backup settings (a
	// created one is left alone: the app's own settings rule).
	SetBackups(ctx context.Context, id string, backups map[string]model.BackupChoice) error
	// SetState moves a pending or created instance to state (deleted,
	// unlinked), clearing its last error.
	SetState(ctx context.Context, id string, state model.InstanceState) error
	// SetError records the last failed command's error.
	SetError(ctx context.Context, id, msg string) error
}

// InstanceLinks is what the app routes know of template instances: an
// app that is a live (pending or created) instance is changed through its
// instance, and deleting it through delete-app retires its key.
type InstanceLinks interface {
	// LiveInstanceForApp reports whether the app on serverID is a live
	// instance, and its template's name ("" when the template is gone).
	LiveInstanceForApp(ctx context.Context, serverID, appID string) (templateName string, ok bool, err error)
	// RetireForApp moves the app's live instance, if any, to deleted: its
	// key stays retired.
	RetireForApp(ctx context.Context, serverID, appID string) error
}
