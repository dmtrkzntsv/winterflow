package template

import (
	"encoding/json"
	"maps"
	"slices"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// Marker is the "instance" key of an instance's config blob: the editor
// and the app page know from it that the app is an instance.
type Marker struct {
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	Key          string `json:"key"`
}

// Input is one render.
type Input struct {
	AppID string
	// Name is the app's name (checked with CheckName).
	Name   string
	Values Values
	// Hosts are the ingress hostnames (checked with CheckHosts).
	Hosts []model.InstanceHost
	// Marker is nil when the app stops being an instance (unlink).
	Marker *Marker
	// Backups, when non-nil, is the whole backups list to store (a create);
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

// config is the rendered config blob. It has no ports: the agent keeps the
// app's host ports (and pins) when a save carries none.
type config struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	Color       string                 `json:"color"`
	Files       []configFile           `json:"files"`
	Variables   []configVar            `json:"variables"`
	Ingress     *model.Ingress         `json:"ingress"`
	TemplateID  string                 `json:"template_id,omitempty"`
	Instance    *Marker                `json:"instance,omitempty"`
	Backups     *[]recipe.BackupConfig `json:"backups,omitempty"`
}

// Render turns a body and an instance's choices into the catalog record and
// the app.save payload, exactly what the app editor would send: the files
// unchanged, one app variable per value (secrets as the caller's ciphertext
// or the keep placeholder; a variable without a value sends nothing, so the
// compose file's own value stands), and a config blob with the hosts as the
// ingress, the template id (the apps.list reconcile keeps apps.template_id
// from it) and the marker. The agent applies x-winterflow itself.
func Render(b Body, in Input) (model.App, command.AppPayload, error) {
	if err := CheckName(in.Name); err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	app := model.App{ID: in.AppID, Name: in.Name, Icon: b.Icon, Color: b.Color}
	cfg := config{Name: in.Name, Icon: b.Icon, Color: b.Color, Files: []configFile{}, Variables: []configVar{}, Ingress: ingressOf(in.Hosts)}
	payload := command.AppPayload{AppID: in.AppID, Files: []command.ContentItem{}, Variables: []command.ContentItem{}}

	for _, name := range slices.Sorted(maps.Keys(b.Files)) {
		payload.Files = append(payload.Files, command.ContentItem{Name: name, Content: []byte(b.Files[name])})
		cfg.Files = append(cfg.Files, configFile{Filename: name})
	}
	for _, name := range slices.Sorted(maps.Keys(in.Values.Plain)) {
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(in.Values.Plain[name])})
		cfg.Variables = append(cfg.Variables, configVar{Name: name})
	}
	for _, name := range slices.Sorted(maps.Keys(in.Values.Secrets)) {
		payload.Variables = append(payload.Variables, command.ContentItem{Name: name, Content: []byte(in.Values.Secrets[name]), Encrypted: true})
		cfg.Variables = append(cfg.Variables, configVar{Name: name, IsEncrypted: true})
	}
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
