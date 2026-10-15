package templates

import (
	"context"
	"errors"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// ErrBackupsNotPending is a backups setting sent for an instance the agent
// already confirmed: its backups belong to the app's Backups tab.
var ErrBackupsNotPending = model.Rejected("backups can only be set while the instance is pending; change them in the app's Backups tab")

// ErrNeedsValues marks an update or upgrade the instance's stored choices
// can't render: a newly required variable without a value, a stored value
// the revision's pattern refuses, a hostname whose expose entry the
// revision no longer has. An "Upgrade all" rollout skips such an instance.
// The error's message is the validation's.
var ErrNeedsValues = errors.New("the instance needs new values for this revision")

// needsValues is a values error that also matches ErrNeedsValues.
type needsValues struct{ err error }

func (e needsValues) Error() string   { return e.err.Error() }
func (e needsValues) Unwrap() []error { return []error{e.err, ErrNeedsValues} }

// UpdateInput is POST update-instance and (with Revision) upgrade-instance.
type UpdateInput struct {
	ServerID string
	Key      string
	Revision int64
	Values   map[string]string
	Secrets  map[string]string
	// Hosts, when non-nil, replace the instance's hostnames.
	Hosts *[]model.InstanceHost
	// Backups are backup settings by name; accepted only while the
	// instance is pending.
	Backups map[string]template.BackupChoice
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
// over the stored ones) and hostnames, and redeploys; with in.Revision set
// it moves to that revision (an upgrade). An upgrade sends no backups
// list: the app keeps its settings, and a backup the revision adds starts
// off.
//
// A pending instance was never confirmed by the agent: the agent holds none
// of its secrets and no backups list. Its render therefore takes every
// secret from the caller (none is kept) and sends the whole backups list
// (in.Backups).
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
	revision := inst.Revision
	if in.Revision != 0 {
		revision = in.Revision
	}
	body, spec, err := uc.revision(ctx, tpl.ID, revision)
	if err != nil {
		return Outcome{}, err
	}
	hosts := inst.Hosts
	if in.Hosts != nil {
		hosts = *in.Hosts
	}
	if err := template.CheckHosts(spec, hosts); err != nil {
		if in.Hosts == nil {
			return Outcome{}, needsValues{err} // the stored hosts no longer fit
		}
		return Outcome{}, err
	}
	stored := template.Stored{Values: inst.Params, SecretNames: inst.SecretNames}
	input := template.Input{
		AppID: inst.AppID, Name: uc.appName(ctx, inst, tpl.Name), Hosts: hosts,
		Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: inst.Key},
	}
	if pending {
		stored.SecretNames = nil
		if input.Backups, err = template.CreateBackups(spec, in.Backups); err != nil {
			return Outcome{}, err
		}
	}
	values, err := template.Resolve(spec, stored, template.Given{Values: in.Values, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, needsValues{err}
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
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values, hosts)
}
