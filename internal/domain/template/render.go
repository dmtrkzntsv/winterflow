package template

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// maxRenderedName caps the app name a render produces (parameter values
// are free text).
const maxRenderedName = 255

// Marker is the "instance" key of an instance's config blob: the editor
// and the app page know from it that the app is an instance. The agent
// stores it, except BackupDefaults, which its save applies and drops.
type Marker struct {
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	Key          string `json:"key"`
	// BackupDefaults are added by the agent's save for backup names the
	// app's backups list has no entry for (backups new in an upgrade).
	BackupDefaults []recipe.BackupConfig `json:"backup_defaults,omitempty"`
}

// Input is one render.
type Input struct {
	AppID  string
	Values Values
	// Marker is nil when the app stops being an instance (unlink).
	Marker *Marker
	// Backups, when non-nil, is the whole backups list to store (create);
	// nil leaves the app's stored list as it is.
	Backups []recipe.BackupConfig
}

// configFile and configVar are the config blob's item metadata (the app
// editor's shape).
type configFile struct {
	Filename    string `json:"filename"`
	IsEncrypted bool   `json:"is_encrypted"`
}

type configVar struct {
	Name        string `json:"name"`
	IsEncrypted bool   `json:"is_encrypted"`
}

// config is the rendered config blob.
type config struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	Color       string                 `json:"color"`
	Files       []configFile           `json:"files"`
	Variables   []configVar            `json:"variables"`
	Ingress     model.Ingress          `json:"ingress"`
	TemplateID  string                 `json:"template_id,omitempty"`
	Instance    *Marker                `json:"instance,omitempty"`
	Backups     *[]recipe.BackupConfig `json:"backups,omitempty"`
}

// Render turns a body and an instance's values into the catalog record and
// the app.save payload, exactly what the app editor would send: the files
// unchanged, one variable per parameter (secrets as the caller's
// ciphertext or the keep placeholder), and a config blob with the rendered
// ingress, the template id (the apps.list reconcile keeps apps.template_id
// from it) and the marker.
func Render(b Body, in Input) (model.App, command.AppPayload, error) {
	plain := in.Values.Plain
	name := expand(b.AppName, plain)
	if strings.TrimSpace(name) == "" || len(name) > maxRenderedName {
		return model.App{}, command.AppPayload{}, Invalid(valueField(b.AppName, "app_name"), "the app name must be 1-%d characters", maxRenderedName)
	}
	app := model.App{ID: in.AppID, Name: name, Icon: b.Icon, Color: b.Color}
	cfg := config{Name: app.Name, Icon: b.Icon, Color: b.Color, Files: []configFile{}, Variables: []configVar{}}
	payload := command.AppPayload{AppID: in.AppID, Files: []command.ContentItem{}, Variables: []command.ContentItem{}}

	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		payload.Files = append(payload.Files, command.ContentItem{Name: name, Content: []byte(b.Files[name])})
		cfg.Files = append(cfg.Files, configFile{Filename: name})
	}
	for _, p := range b.Parameters {
		if p.Secret {
			ct, ok := in.Values.Secrets[p.Name]
			if !ok {
				continue
			}
			payload.Variables = append(payload.Variables, command.ContentItem{Name: p.Name, Content: []byte(ct), Encrypted: true})
			cfg.Variables = append(cfg.Variables, configVar{Name: p.Name, IsEncrypted: true})
			continue
		}
		payload.Variables = append(payload.Variables, command.ContentItem{Name: p.Name, Content: []byte(plain[p.Name])})
		cfg.Variables = append(cfg.Variables, configVar{Name: p.Name})
	}
	rows, err := renderIngress(b.Ingress, plain, false)
	if err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	cfg.Ingress = model.Ingress{Domains: rows, Redirects: []model.IngressRedirect{}}
	if in.Marker != nil {
		app.TemplateID = in.Marker.TemplateID
		cfg.TemplateID = in.Marker.TemplateID
		cfg.Instance = in.Marker
	}
	if in.Backups != nil {
		cfg.Backups = &in.Backups
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	payload.Config = raw
	return app, payload, nil
}

// CreateBackups is the backups list a new instance starts with: each
// template default by name, its storage replaced by the call's override.
// An override must name a backup the template has a default for.
func CreateBackups(b Body, storages map[string]string) ([]recipe.BackupConfig, error) {
	for _, name := range slices.Sorted(maps.Keys(storages)) {
		if _, ok := b.Backups[name]; !ok {
			return nil, Invalid("backups."+name, "the template has no backup default %q", name)
		}
		if err := model.ValidateStorageName(storages[name]); err != nil {
			return nil, Invalid("backups."+name+".storage", "%v", err)
		}
	}
	out := []recipe.BackupConfig{}
	for _, name := range slices.Sorted(maps.Keys(b.Backups)) {
		c := b.Backups[name].config(name)
		if s, ok := storages[name]; ok {
			c.Storage = s
		}
		out = append(out, c)
	}
	return out, nil
}

// NewBackupDefaults are the defaults of next for backups prev's compose
// files don't declare: an upgrade adds them (the agent merges them only
// where the app has no entry). Backups both revisions declare keep the
// app's settings, Backups-tab changes included.
func NewBackupDefaults(prev, next Body) ([]recipe.BackupConfig, error) {
	old, err := prev.RecipeNames()
	if err != nil {
		return nil, err
	}
	var out []recipe.BackupConfig
	for _, name := range slices.Sorted(maps.Keys(next.Backups)) {
		if !old[name] {
			out = append(out, next.Backups[name].config(name))
		}
	}
	return out, nil
}
