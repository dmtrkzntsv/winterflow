package templates

import (
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// InstanceView is an instance as the API shows it: never a secret value.
type InstanceView struct {
	Key          string `json:"key"`
	ServerID     string `json:"server_id"`
	AppID        string `json:"app_id"`
	TemplateID   string `json:"template_id" description:"Empty once the template is deleted."`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	// Parameters are the values callers set; parameters left out use the
	// revision's default.
	Parameters map[string]string `json:"parameters" description:"Values the callers set; the others use the revision's default."`
	Secrets    map[string]string `json:"secrets" description:"Each secret given, as \"<encrypted>\": values are never returned."`
	State      string            `json:"state" description:"pending, created, deleted or unlinked (only an unlink's own answer shows unlinked: the key then finds nothing)."`
	// StatusCode is the app's latest container status (0 unknown).
	StatusCode command.ContainerStatusCode `json:"status_code"`
	LastError  string                      `json:"last_error" description:"The last failed command's error; empty after a success."`
	CreatedAt  time.Time                   `json:"created_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
}

// InstanceList is the data of GET get-instances.
type InstanceList struct {
	Instances []InstanceView `json:"instances"`
}

// view builds an instance's view; templateName is "" when it is gone.
func (uc *UseCase) view(inst model.Instance, templateName string) InstanceView {
	secrets := map[string]string{}
	for _, n := range inst.SecretNames {
		secrets[n] = command.EncryptedPlaceholder
	}
	params := inst.Params
	if params == nil {
		params = map[string]string{}
	}
	v := InstanceView{
		Key: inst.Key, ServerID: inst.ServerID, AppID: inst.AppID, TemplateID: inst.TemplateID,
		TemplateName: templateName, Revision: inst.Revision, Parameters: params, Secrets: secrets,
		State: string(inst.State), LastError: inst.LastError, CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
	}
	if uc.status != nil {
		for _, st := range uc.status.AppStatuses(inst.ServerID, time.Now()) {
			if st.AppID == inst.AppID {
				v.StatusCode = st.StatusCode
			}
		}
	}
	return v
}
