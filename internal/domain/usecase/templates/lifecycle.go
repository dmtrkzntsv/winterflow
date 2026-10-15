package templates

import (
	"context"
	"errors"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// ControlInstance starts, stops or restarts the instance's app.
func (uc *UseCase) ControlInstance(ctx context.Context, userID, orgID, serverID, key string, action command.ControlAction) (Outcome, error) {
	switch action {
	case command.ControlStart, command.ControlStop, command.ControlRestart:
	default:
		return Outcome{}, template.Invalid("action", "one of start, stop, restart")
	}
	inst, err := uc.live(ctx, orgID, serverID, key)
	if err != nil {
		return Outcome{}, err
	}
	observer, done := uc.observe(inst, nil)
	requestID, err := uc.apps.ControlAppObserved(ctx, userID, serverID, inst.AppID, action, observer)
	return outcome(requestID, err, done, uc.reread(inst))
}

// DeleteInstance deletes the instance's app (and, as asked, its volumes and
// backups); on success the key is retired. A deleted instance answers as
// it is.
func (uc *UseCase) DeleteInstance(ctx context.Context, userID, orgID, serverID, key string, deleteVolumes, deleteBackups bool) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, serverID, key)
	if errors.Is(err, model.ErrKeyRetired) {
		v := uc.view(inst, uc.templateName(ctx, orgID, inst.TemplateID))
		return Outcome{Instance: &v}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	id := inst.ID
	observer, done := uc.observe(inst, func(ctx context.Context) error {
		return uc.repo.SetState(ctx, id, model.InstanceDeleted)
	})
	requestID, err := uc.apps.DeleteAppObserved(ctx, userID, serverID, inst.AppID, deleteVolumes, deleteBackups, observer)
	return outcome(requestID, err, done, uc.reread(inst))
}

// UnlinkInstance saves the app as an ordinary app (no marker, no template
// id) at its current revision, values and hostnames; on success the key is
// free.
func (uc *UseCase) UnlinkInstance(ctx context.Context, userID, orgID, serverID, key string) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, serverID, key)
	if err != nil {
		return Outcome{}, err
	}
	if inst.State != model.InstanceCreated {
		return Outcome{}, model.ErrInstanceNotCreated
	}
	body, spec, err := uc.revision(ctx, inst.TemplateID, inst.Revision)
	if err != nil {
		return Outcome{}, err
	}
	values, err := template.Resolve(spec, template.Stored{Values: inst.Params, SecretNames: inst.SecretNames}, template.Given{})
	if err != nil {
		return Outcome{}, err
	}
	name := uc.templateName(ctx, orgID, inst.TemplateID)
	app, payload, err := template.Render(body, template.Input{
		AppID: inst.AppID, Name: uc.appName(ctx, inst, name), Values: values, Hosts: inst.Hosts,
	})
	if err != nil {
		return Outcome{}, err
	}
	checked, err := uc.apps.CheckSave(ctx, serverID, app, payload, false)
	if err != nil {
		return Outcome{}, err
	}
	id := inst.ID
	observer, done := uc.observe(inst, func(ctx context.Context) error {
		return uc.repo.SetState(ctx, id, model.InstanceUnlinked)
	})
	requestID, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
	if err != nil {
		return Outcome{}, err
	}
	final := func(ctx context.Context) (InstanceView, error) {
		// The key no longer finds an unlinked instance: read it by id.
		got, err := uc.repo.GetInstanceByID(ctx, id)
		if err != nil {
			return InstanceView{}, err
		}
		return uc.view(got, name), nil
	}
	return outcome(requestID, nil, done, final)
}
