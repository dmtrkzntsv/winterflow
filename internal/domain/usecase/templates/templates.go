package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	"winterflow/pkg/util"
)

// SaveInput is POST save-template: no ID creates the template (revision
// 1, BaseRevision 0); an ID adds revision BaseRevision+1.
type SaveInput struct {
	ID           string
	Name         string
	Body         template.Body
	Note         string
	BaseRevision int64
}

// maxNote caps a revision note.
const maxNote = 1000

// SaveTemplate validates the body and stores it as a new revision of an
// org template (a catalog one is model.ErrCatalogReadOnly). The warnings
// are computed before the write, so an error never follows a stored
// revision.
func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in SaveInput) (SavedTemplate, error) {
	if in.ID != "" {
		if err := uc.requireOwn(ctx, orgID, in.ID); err != nil {
			return SavedTemplate{}, err
		}
	}
	if err := template.ValidateName(in.Name); err != nil {
		return SavedTemplate{}, err
	}
	if len(in.Note) > maxNote {
		return SavedTemplate{}, template.Invalid("note", "at most %d characters", maxNote)
	}
	spec, err := template.Validate(in.Body)
	if err != nil {
		return SavedTemplate{}, err
	}
	raw, err := template.Encode(in.Body)
	if err != nil {
		return SavedTemplate{}, err
	}
	warnings, err := uc.warnings(ctx, orgID, in.Body, spec)
	if err != nil {
		return SavedTemplate{}, err
	}
	rev := model.TemplateRevision{Body: raw, Note: in.Note, CreatedBy: userID}
	out := SavedTemplate{ID: in.ID, Name: in.Name, Warnings: warnings}
	if in.ID == "" {
		if in.BaseRevision != 0 {
			return SavedTemplate{}, template.Invalid("base_revision", "a new template has none (0)")
		}
		out.ID, out.Revision = util.GenerateID(), 1
		err = uc.repo.CreateTemplate(ctx, model.Template{ID: out.ID, Source: model.TemplateSourceOrg, OrgID: orgID, Name: in.Name, CreatedBy: userID}, rev)
	} else {
		out.Revision, err = uc.repo.AddRevision(ctx, orgID, in.ID, in.Name, in.BaseRevision, rev)
	}
	if err != nil {
		return SavedTemplate{}, err
	}
	return out, nil
}

// InspectTemplate reads a body as save-template would, without storing
// anything: what its compose files declare (as far as they parse) and the
// first problem a save would refuse ("" when none). The editor's Detected
// panel follows the files with it.
func (uc *UseCase) InspectTemplate(ctx context.Context, orgID string, b template.Body) (Inspection, error) {
	spec, err := template.Validate(b)
	if err == nil {
		_, err = template.Encode(b)
	}
	out := Inspection{}
	if err != nil {
		if !errors.Is(err, model.ErrRejected) {
			return Inspection{}, err
		}
		out.Problem = err.Error()
		// What parses still shows: a bad summary leaves the block readable.
		// Files that don't parse declare nothing, so every reference
		// would read as undeclared: the problem is all there is to say.
		if spec, err = template.Parse(b); err != nil {
			out.Detected = template.Detect(xwinterflow.Spec{}, nil)
			return out, nil
		}
	}
	warnings, err := uc.warnings(ctx, orgID, b, spec)
	if err != nil {
		return Inspection{}, err
	}
	out.Detected = template.Detect(spec, warnings)
	return out, nil
}

// warnings are what a save of b points out (template.Warnings, the org's
// shared variables counting as provided).
func (uc *UseCase) warnings(ctx context.Context, orgID string, b template.Body, spec xwinterflow.Spec) ([]string, error) {
	shared, err := uc.sharedNames(ctx, orgID)
	if err != nil {
		return nil, err
	}
	return template.Warnings(b, spec, func(name string) bool { return shared[name] }), nil
}

// requireOwn refuses a change to a catalog template (model.ErrCatalogReadOnly,
// a 403); one the org can't read is model.ErrTemplateNotFound.
func (uc *UseCase) requireOwn(ctx context.Context, orgID, id string) error {
	t, err := uc.repo.GetTemplate(ctx, orgID, id)
	if err != nil {
		return err
	}
	if t.Catalog() {
		return model.ErrCatalogReadOnly
	}
	return nil
}

// sharedNames are the org's shared variable names.
func (uc *UseCase) sharedNames(ctx context.Context, orgID string) (map[string]bool, error) {
	out := map[string]bool{}
	if uc.shared == nil {
		return out, nil
	}
	vars, err := uc.shared.ListOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, v := range vars {
		out[v.Name] = true
	}
	return out, nil
}

// ListTemplates lists the org's templates, then the catalog's, with the
// org's instance counts.
func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateList, error) {
	list, err := uc.repo.ListTemplates(ctx, orgID)
	if err != nil {
		return TemplateList{}, err
	}
	out := TemplateList{Templates: make([]TemplateSummary, 0, len(list))}
	for _, t := range list {
		out.Templates = append(out.Templates, TemplateSummary{
			ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision,
			Instances: t.Instances, Behind: t.Behind, WithdrawnAt: t.WithdrawnAt, UpdatedAt: t.UpdatedAt,
		})
	}
	return out, nil
}

// GetTemplate returns a revision (0: the latest) with what its compose
// files declare and the revision list.
func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision int64) (TemplateView, error) {
	t, err := uc.repo.GetTemplate(ctx, orgID, id)
	if err != nil {
		return TemplateView{}, err
	}
	if revision == 0 {
		revision = t.LatestRevision
	}
	rev, err := uc.repo.GetRevision(ctx, id, revision)
	if err != nil {
		return TemplateView{}, err
	}
	body, err := decodeRevision(rev)
	if err != nil {
		return TemplateView{}, err
	}
	// A stored body passed the checks of its day; one a later parser
	// refuses still reads, its problem shown as a warning.
	detected := template.Detect(xwinterflow.Spec{}, nil)
	if spec, err := template.Parse(body); err != nil {
		detected.Warnings = []string{err.Error()}
	} else {
		detected = template.Detect(spec, spec.Warnings)
	}
	revs, err := uc.repo.ListRevisions(ctx, id)
	if err != nil {
		return TemplateView{}, err
	}
	out := TemplateView{
		ID: t.ID, Source: t.Source, Slug: t.Slug, Name: t.Name, LatestRevision: t.LatestRevision, WithdrawnAt: t.WithdrawnAt,
		Revision: rev.Revision, Body: body, Detected: detected, Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy,
		Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
	}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, TemplateRevisionInfo{Revision: r.Revision, Note: r.Note, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy})
	}
	return out, nil
}

// revision loads one stored revision's body and what its compose files
// declare.
func (uc *UseCase) revision(ctx context.Context, templateID string, revision int64) (template.Body, xwinterflow.Spec, error) {
	rev, err := uc.repo.GetRevision(ctx, templateID, revision)
	if err != nil {
		return template.Body{}, xwinterflow.Spec{}, err
	}
	body, err := decodeRevision(rev)
	if err != nil {
		return template.Body{}, xwinterflow.Spec{}, err
	}
	spec, err := template.Parse(body)
	if err != nil {
		return template.Body{}, xwinterflow.Spec{}, fmt.Errorf("template %s r%d: %w", templateID, revision, err)
	}
	return body, spec, nil
}

// decodeRevision decodes a stored revision's body.
func decodeRevision(rev model.TemplateRevision) (template.Body, error) {
	var body template.Body
	if err := json.Unmarshal(rev.Body, &body); err != nil {
		return template.Body{}, fmt.Errorf("template %s r%d: %w", rev.TemplateID, rev.Revision, err)
	}
	return body, nil
}

// DeleteTemplate deletes an org template without live instances (a
// catalog one is model.ErrCatalogReadOnly).
func (uc *UseCase) DeleteTemplate(ctx context.Context, orgID, id string) error {
	if err := uc.requireOwn(ctx, orgID, id); err != nil {
		return err
	}
	return uc.repo.DeleteTemplate(ctx, orgID, id)
}
