package templates

import (
	"context"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// ErrBackupsNotPending is a backups override sent for an instance the
// agent already confirmed: its backups belong to the app's Backups tab.
var ErrBackupsNotPending = model.Rejected("backups can only be set while the instance is pending; change them in the app's Backups tab")

// UpdateInput is POST update-instance and (with Revision) upgrade-instance.
type UpdateInput struct {
	ServerID string
	Key      string
	Revision int64
	Params   map[string]string
	Secrets  map[string]string
	// Backups overrides the storage of template backup defaults by name;
	// accepted only while the instance is pending.
	Backups map[string]string
}

// live returns the org's instance with key on serverID: model.ErrInstanceNotFound
// when there is none (or it is on another server), model.ErrKeyRetired when
// it was deleted.
func (uc *UseCase) live(ctx context.Context, orgID, serverID, key string) (model.Instance, error) {
	inst, err := uc.repo.GetInstance(ctx, orgID, key)
	if err != nil {
		return model.Instance{}, err
	}
	if inst.ServerID != serverID {
		return model.Instance{}, model.ErrInstanceNotFound
	}
	if inst.State == model.InstanceDeleted {
		return inst, model.ErrKeyRetired
	}
	return inst, nil
}

// UpdateInstance re-renders the instance's revision with new values (merged
// over the stored ones) and redeploys; with in.Revision set it moves to
// that revision (an upgrade), adding the backup defaults of backups the
// new revision brings.
//
// A pending instance was never confirmed by the agent: the agent holds none
// of its secrets and no backups list. Its render therefore takes every
// secret from the caller (none is kept) and sends the whole backups list
// (the template defaults plus in.Backups) instead of marker defaults.
func (uc *UseCase) UpdateInstance(ctx context.Context, userID, orgID string, in UpdateInput) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, in.ServerID, in.Key)
	if err != nil {
		return Outcome{}, err
	}
	pending := inst.State == model.InstancePending
	if len(in.Backups) > 0 && !pending {
		return Outcome{}, ErrBackupsNotPending
	}
	tpl, err := uc.repo.GetTemplate(ctx, orgID, inst.TemplateID)
	if err != nil {
		return Outcome{}, err
	}
	_, current, err := uc.revision(ctx, tpl.ID, inst.Revision)
	if err != nil {
		return Outcome{}, err
	}
	revision, body := inst.Revision, current
	marker := &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Key: inst.Key}
	upgrade := in.Revision != 0 && in.Revision != inst.Revision
	if upgrade {
		if _, body, err = uc.revision(ctx, tpl.ID, in.Revision); err != nil {
			return Outcome{}, err
		}
		revision = in.Revision
		if !pending {
			if marker.BackupDefaults, err = template.NewBackupDefaults(current, body); err != nil {
				return Outcome{}, err
			}
		}
	}
	marker.Revision = revision
	stored := template.Stored{Params: inst.Params, SecretNames: inst.SecretNames}
	input := template.Input{AppID: inst.AppID, Marker: marker}
	if pending {
		stored.SecretNames = nil
		if input.Backups, err = template.CreateBackups(body, in.Backups); err != nil {
			return Outcome{}, err
		}
	}
	values, err := template.Resolve(body, stored, template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, err
	}
	input.Values = values
	app, payload, err := template.Render(body, input)
	if err != nil {
		return Outcome{}, err
	}
	checked, err := uc.apps.CheckSave(ctx, inst.ServerID, app, payload, false)
	if err != nil {
		return Outcome{}, err
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}
