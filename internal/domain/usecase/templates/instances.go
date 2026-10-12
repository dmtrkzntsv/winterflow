package templates

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/util"
)

// keyRE is an instance key: the caller's own id for the instance.
var keyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// ValidateKey checks an instance key.
func ValidateKey(key string) error {
	if !keyRE.MatchString(key) {
		return fmt.Errorf("%w: key: 1-128 letters, digits and . _ : @ -, starting with a letter or digit", template.ErrInvalid)
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
	Params   map[string]string
	Secrets  map[string]string
	// Backups overrides the storage of template backup defaults by name.
	Backups map[string]string
}

// CreateInstance creates the instance, or replays a create the key already
// made: the same request answers the created instance (nothing is
// dispatched) or dispatches a pending one's save again; a different
// request is model.ErrKeyInUse, a deleted instance's key
// model.ErrKeyRetired. The key is claimed only once the rendered app passes
// every save-app check.
func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in CreateInput) (Outcome, error) {
	if err := ValidateKey(in.Key); err != nil {
		return Outcome{}, err
	}
	tpl, err := uc.repo.GetTemplate(ctx, orgID, in.TemplateID)
	if err != nil {
		return Outcome{}, err
	}
	revision := in.Revision
	if revision == 0 {
		revision = tpl.LatestRevision
	}
	_, body, err := uc.revision(ctx, tpl.ID, revision)
	if err != nil {
		return Outcome{}, err
	}
	values, err := template.Resolve(body, template.Stored{}, template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, err
	}
	backups, err := template.CreateBackups(body, in.Backups)
	if err != nil {
		return Outcome{}, err
	}
	hash := template.RequestHash(template.HashInput{
		ServerID: in.ServerID, TemplateID: tpl.ID, Revision: in.Revision,
		Params: in.Params, SecretNames: sortedNames(in.Secrets), Backups: in.Backups,
	})
	render := func(appID string) (appuc.CheckedSave, error) {
		app, payload, err := template.Render(body, template.Input{
			AppID: appID, Values: values, Backups: backups,
			Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: in.Key},
		})
		if err != nil {
			return appuc.CheckedSave{}, err
		}
		return uc.apps.CheckSave(ctx, in.ServerID, app, payload, false)
	}

	existing, err := uc.repo.GetInstance(ctx, orgID, in.Key)
	switch {
	case err == nil:
		return uc.replay(ctx, userID, existing, tpl.Name, hash, revision, values, render)
	case !errors.Is(err, model.ErrNotFound):
		return Outcome{}, err
	}
	checked, err := render(util.GenerateID())
	if err != nil {
		return Outcome{}, err
	}
	inst, inserted, err := uc.repo.ClaimInstance(ctx, model.Instance{
		ID: util.GenerateID(), OrgID: orgID, Key: in.Key, AppID: checked.AppID(), ServerID: in.ServerID,
		TemplateID: tpl.ID, Revision: revision, Params: values.Stored.Params, SecretNames: values.Stored.SecretNames,
		RequestHash: hash,
	})
	if err != nil {
		return Outcome{}, err
	}
	if !inserted {
		// Another create claimed the key in between.
		return uc.replay(ctx, userID, inst, tpl.Name, hash, revision, values, render)
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}

// replay answers a create whose key is taken.
func (uc *UseCase) replay(ctx context.Context, userID string, inst model.Instance, templateName, hash string, revision int64, values template.Values, render func(string) (appuc.CheckedSave, error)) (Outcome, error) {
	switch {
	case inst.State == model.InstanceDeleted:
		return Outcome{}, model.ErrKeyRetired
	case inst.RequestHash != hash:
		return Outcome{}, model.ErrKeyInUse
	case inst.State == model.InstanceCreated:
		v := uc.view(inst, templateName)
		return Outcome{Instance: &v}, nil
	}
	checked, err := render(inst.AppID)
	if err != nil {
		return Outcome{}, err
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}

// dispatchSave dispatches a rendered save; its confirmation records the
// revision and values (state created), a failure the error.
func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.Instance, checked appuc.CheckedSave, revision int64, values template.Values) (Outcome, error) {
	// The hook lives as long as the result is awaited: it keeps only what
	// it records, not the secret ciphertexts.
	id, stored := inst.ID, values.Stored
	observer, done := uc.observe(inst, func(ctx context.Context, res port.CommandResult) error {
		if !res.Success {
			return uc.repo.SetError(ctx, id, res.Error)
		}
		return uc.repo.MarkSaved(ctx, id, revision, stored.Params, stored.SecretNames)
	})
	requestID, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RequestID: requestID, Done: done, Final: uc.reread(inst)}, nil
}

// reread is an Outcome's Final for an instance that keeps its key.
func (uc *UseCase) reread(inst model.Instance) func(ctx context.Context) (InstanceView, error) {
	return func(ctx context.Context) (InstanceView, error) {
		return uc.GetInstance(ctx, inst.OrgID, inst.ServerID, inst.Key)
	}
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
// to a server and/or a template when given (another org's template is
// model.ErrTemplateNotFound).
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

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
