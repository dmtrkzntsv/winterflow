package templates

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"slices"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/util"
)

// keyRE is an instance key: the caller's own id for the instance.
var keyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// ValidateKey checks an instance key.
func ValidateKey(key string) error {
	if !keyRE.MatchString(key) {
		return template.Invalid("key", "1-128 letters, digits and . _ : @ -, starting with a letter or digit")
	}
	return nil
}

// CreateInput is POST create-instance.
type CreateInput struct {
	ServerID   string
	Key        string
	TemplateID string
	// Revision 0 is the latest.
	Revision int64
	// Name is the app's name; "" is the template's name.
	Name    string
	Values  map[string]string
	Secrets map[string]string
	Hosts   []model.InstanceHost
	// Backups are the settings of the backups the compose file declares,
	// by name; a backup without one starts off.
	Backups map[string]template.BackupChoice
}

// CreateInstance creates the instance, or replays a create the key already
// made: the same request answers the created instance (nothing is
// dispatched, nothing is checked again: a later revision or a withdrawn
// template changes nothing) or dispatches a pending one's save again, at
// the revision and with the name it was claimed with; a different request
// is model.ErrKeyInUse, a deleted instance's key model.ErrKeyRetired. The
// key is claimed only once the rendered app passes every save-app check. A
// withdrawn catalog template takes no new installs and completes no
// pending one (model.ErrTemplateWithdrawn).
func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in CreateInput) (Outcome, error) {
	if err := ValidateKey(in.Key); err != nil {
		return Outcome{}, err
	}
	tpl, err := uc.repo.GetTemplate(ctx, orgID, in.TemplateID)
	if err != nil {
		return Outcome{}, err
	}
	hash := template.RequestHash(template.HashInput{
		ServerID: in.ServerID, TemplateID: tpl.ID, Revision: in.Revision, Name: in.Name,
		Values: in.Values, SecretNames: slices.Collect(maps.Keys(in.Secrets)), Hosts: in.Hosts, Backups: in.Backups,
	})
	existing, err := uc.repo.GetInstance(ctx, orgID, in.Key)
	switch {
	case err == nil:
		return uc.replay(ctx, userID, existing, tpl, hash, in)
	case !errors.Is(err, model.ErrNotFound):
		return Outcome{}, err
	}
	if tpl.WithdrawnAt != nil {
		return Outcome{}, model.ErrTemplateWithdrawn
	}
	revision := in.Revision
	if revision == 0 {
		revision = tpl.LatestRevision
	}
	name := in.Name
	if name == "" {
		name = tpl.Name
	}
	c, err := uc.prepareCreate(ctx, tpl, revision, name, in)
	if err != nil {
		return Outcome{}, err
	}
	checked, err := c.render(util.GenerateID())
	if err != nil {
		return Outcome{}, err
	}
	inst, inserted, err := uc.repo.ClaimInstance(ctx, model.Instance{
		ID: util.GenerateID(), OrgID: orgID, Key: in.Key, AppID: checked.AppID(), ServerID: in.ServerID,
		TemplateID: tpl.ID, Revision: revision, Name: name, Params: c.values.Stored.Values, SecretNames: c.values.Stored.SecretNames,
		Hosts: in.Hosts, Backups: in.Backups, RequestHash: hash,
	})
	if err != nil {
		return Outcome{}, err
	}
	if !inserted {
		// Another create claimed the key in between.
		return uc.replay(ctx, userID, inst, tpl, hash, in)
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, c.values, in.Hosts)
}

// createRender is a create checked against one revision: its resolved
// values and the render of its app (by app id).
type createRender struct {
	values template.Values
	render func(appID string) (appuc.CheckedSave, error)
}

// prepareCreate checks a create against the revision and returns its
// render.
func (uc *UseCase) prepareCreate(ctx context.Context, tpl model.Template, revision int64, name string, in CreateInput) (createRender, error) {
	body, spec, err := uc.revision(ctx, tpl.ID, revision)
	if err != nil {
		return createRender{}, err
	}
	if err := template.CheckName(name); err != nil {
		return createRender{}, err
	}
	if err := template.CheckHosts(spec, in.Hosts); err != nil {
		return createRender{}, err
	}
	values, err := template.Resolve(spec, template.Stored{}, template.Given{Values: in.Values, Secrets: in.Secrets})
	if err != nil {
		return createRender{}, err
	}
	backups, err := template.CreateBackups(spec, in.Backups)
	if err != nil {
		return createRender{}, err
	}
	return createRender{values: values, render: func(appID string) (appuc.CheckedSave, error) {
		app, payload, err := template.Render(body, template.Input{
			AppID: appID, Name: name, Values: values, Hosts: in.Hosts, Backups: backups,
			Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: in.Key},
		})
		if err != nil {
			return appuc.CheckedSave{}, err
		}
		return uc.apps.CheckSave(ctx, in.ServerID, app, payload, false)
	}}, nil
}

// replay answers a create whose key is taken. A pending instance's save is
// dispatched again at the revision it was claimed at, with the name it was
// claimed with (the same request: its hash matched).
func (uc *UseCase) replay(ctx context.Context, userID string, inst model.Instance, tpl model.Template, hash string, in CreateInput) (Outcome, error) {
	switch {
	case inst.State == model.InstanceDeleted:
		return Outcome{}, model.ErrKeyRetired
	case inst.RequestHash != hash:
		return Outcome{}, model.ErrKeyInUse
	case inst.State == model.InstanceCreated:
		v := uc.view(inst, tpl.Name)
		return Outcome{Instance: &v}, nil
	case tpl.WithdrawnAt != nil:
		return Outcome{}, model.ErrTemplateWithdrawn
	}
	name := inst.Name
	if name == "" {
		name = tpl.Name
	}
	c, err := uc.prepareCreate(ctx, tpl, inst.Revision, name, in)
	if err != nil {
		return Outcome{}, err
	}
	checked, err := c.render(inst.AppID)
	if err != nil {
		return Outcome{}, err
	}
	return uc.dispatchSave(ctx, userID, inst, checked, inst.Revision, c.values, inst.Hosts)
}

// dispatchSave dispatches a rendered save; its confirmation records the
// revision, values and hosts (state created), a failure the error.
func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.Instance, checked appuc.CheckedSave, revision int64, values template.Values, hosts []model.InstanceHost) (Outcome, error) {
	// The hook lives as long as the result is awaited: it keeps only what
	// it records, not the secret ciphertexts.
	id, stored := inst.ID, values.Stored
	observer, done := uc.observe(inst, func(ctx context.Context) error {
		return uc.repo.MarkSaved(ctx, id, revision, stored.Values, stored.SecretNames, hosts)
	})
	requestID, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
	return outcome(requestID, err, done, uc.reread(inst))
}

// reread is an Outcome's Final for an instance that keeps its key.
func (uc *UseCase) reread(inst model.Instance) func(ctx context.Context) (InstanceView, error) {
	return func(ctx context.Context) (InstanceView, error) {
		return uc.GetInstance(ctx, inst.OrgID, inst.ServerID, inst.Key)
	}
}

// appName is the name a render of inst's app uses: the app's current name
// in the API's app list (a rename since the create counts), else the name
// the create asked for, else fallback (the template's).
func (uc *UseCase) appName(ctx context.Context, inst model.Instance, fallback string) string {
	if apps, err := uc.apps.GetApps(ctx, inst.ServerID); err == nil {
		for _, a := range apps {
			if a.ID == inst.AppID && a.Name != "" {
				return a.Name
			}
		}
	} else {
		uc.log.Error("instance: read the app's name", "error", err, "app_id", inst.AppID)
	}
	if inst.Name != "" {
		return inst.Name
	}
	return fallback
}

// GetInstance returns the instance with key on serverID (a deleted one
// too: its key stays retired).
func (uc *UseCase) GetInstance(ctx context.Context, orgID, serverID, key string) (InstanceView, error) {
	inst, err := uc.repo.GetInstance(ctx, orgID, key)
	if err != nil {
		return InstanceView{}, err
	}
	if inst.ServerID != serverID {
		return InstanceView{}, model.ErrInstanceNotFound
	}
	return uc.view(inst, uc.templateName(ctx, orgID, inst.TemplateID)), nil
}

// ListInstances lists the org's pending and created instances, narrowed
// to a server and/or a template when given (a template the org can't read
// is model.ErrTemplateNotFound).
func (uc *UseCase) ListInstances(ctx context.Context, orgID, serverID, templateID string) (InstanceList, error) {
	names, err := uc.repo.ListTemplateNames(ctx, orgID)
	if err != nil {
		return InstanceList{}, err
	}
	if _, ok := names[templateID]; templateID != "" && !ok {
		return InstanceList{}, model.ErrTemplateNotFound
	}
	all, err := uc.repo.ListInstances(ctx, orgID)
	if err != nil {
		return InstanceList{}, err
	}
	out := InstanceList{Instances: []InstanceView{}}
	codes := map[string]map[string]command.ContainerStatusCode{} // by server
	for _, inst := range all {
		if (serverID != "" && inst.ServerID != serverID) || (templateID != "" && inst.TemplateID != templateID) {
			continue
		}
		byApp, ok := codes[inst.ServerID]
		if !ok {
			byApp = uc.statusCodes(inst.ServerID)
			codes[inst.ServerID] = byApp
		}
		out.Instances = append(out.Instances, buildView(inst, names[inst.TemplateID], byApp))
	}
	return out, nil
}

// templateName is the template's name, "" when it is gone or unreadable.
func (uc *UseCase) templateName(ctx context.Context, orgID, id string) string {
	if id == "" {
		return ""
	}
	t, err := uc.repo.GetTemplate(ctx, orgID, id)
	if err != nil {
		return ""
	}
	return t.Name
}
