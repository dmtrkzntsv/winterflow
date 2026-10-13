package templates

import (
	"context"
	"encoding/json"
	"fmt"

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

// SaveTemplate validates the body and stores it as a new revision. The
// warnings name file references nothing provides; they are computed before
// the write, so an error never follows a stored revision.
func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in SaveInput) (SavedTemplate, error) {
	if err := template.ValidateName(in.Name); err != nil {
		return SavedTemplate{}, err
	}
	if len(in.Note) > maxNote {
		return SavedTemplate{}, template.Invalid("note", "at most %d characters", maxNote)
	}
	if err := template.Validate(in.Body); err != nil {
		return SavedTemplate{}, err
	}
	raw, err := template.Encode(in.Body)
	if err != nil {
		return SavedTemplate{}, err
	}
	shared, err := uc.sharedNames(ctx, orgID)
	if err != nil {
		return SavedTemplate{}, err
	}
	rev := model.TemplateRevision{Body: raw, Note: in.Note, CreatedBy: userID}
	out := SavedTemplate{ID: in.ID, Name: in.Name}
	out.Warnings = template.Warnings(in.Body, func(name string) bool { return shared[name] })
	if in.ID == "" {
		if in.BaseRevision != 0 {
			return SavedTemplate{}, template.Invalid("base_revision", "a new template has none (0)")
		}
		out.ID, out.Revision = util.GenerateID(), 1
		err = uc.repo.CreateTemplate(ctx, model.Template{ID: out.ID, OrgID: orgID, Name: in.Name, CreatedBy: userID}, rev)
	} else {
		out.Revision, err = uc.repo.AddRevision(ctx, orgID, in.ID, in.Name, in.BaseRevision, rev)
	}
	if err != nil {
		return SavedTemplate{}, err
	}
	return out, nil
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

// ListTemplates lists the org's templates with instance counts.
func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateList, error) {
	list, err := uc.repo.ListTemplates(ctx, orgID)
	if err != nil {
		return TemplateList{}, err
	}
	out := TemplateList{Templates: make([]TemplateSummary, 0, len(list))}
	for _, t := range list {
		out.Templates = append(out.Templates, TemplateSummary{
			ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Instances: t.Instances, Behind: t.Behind, UpdatedAt: t.UpdatedAt,
		})
	}
	return out, nil
}

// GetTemplate returns a revision (0: the latest) with the revision list.
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
	revs, err := uc.repo.ListRevisions(ctx, id)
	if err != nil {
		return TemplateView{}, err
	}
	out := TemplateView{
		ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Revision: rev.Revision, Body: body,
		Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy, Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
	}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, TemplateRevisionInfo{Revision: r.Revision, Note: r.Note, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy})
	}
	return out, nil
}

// revision loads and decodes one stored revision's body.
func (uc *UseCase) revision(ctx context.Context, templateID string, revision int64) (template.Body, error) {
	rev, err := uc.repo.GetRevision(ctx, templateID, revision)
	if err != nil {
		return template.Body{}, err
	}
	return decodeRevision(rev)
}

// decodeRevision decodes a stored revision's body.
func decodeRevision(rev model.TemplateRevision) (template.Body, error) {
	var body template.Body
	if err := json.Unmarshal(rev.Body, &body); err != nil {
		return template.Body{}, fmt.Errorf("template %s r%d: %w", rev.TemplateID, rev.Revision, err)
	}
	return body, nil
}

// DeleteTemplate deletes a template without live instances.
func (uc *UseCase) DeleteTemplate(ctx context.Context, orgID, id string) error {
	return uc.repo.DeleteTemplate(ctx, orgID, id)
}
