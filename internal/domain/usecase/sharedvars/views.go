package sharedvars

import (
	"context"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// VariableView is one shared variable as the API shows it. A secret's
// Value is always "<encrypted>"; MissingOn lists the keyed servers in its
// scope that hold no copy.
type VariableView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope"`
	IsSecret    bool      `json:"is_secret"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   string    `json:"updated_by"`
	MissingOn   []string  `json:"missing_on"`
}

// InheritedVariable is an org variable seen from one server; Overridden
// means a server variable of the same name wins there.
type InheritedVariable struct {
	VariableView
	Overridden bool `json:"overridden"`
}

// BuiltinVariable is one read-only WF_ variable. Value is "" for the ones
// only the agent knows (per app, data dir, timezone).
type BuiltinVariable struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// ServerSync is one server's shared-variables sync state.
type ServerSync struct {
	ServerID      string `json:"server_id"`
	Name          string `json:"name"`
	HasKey        bool   `json:"has_key"`
	Revision      int64  `json:"revision"`
	AgentRevision *int64 `json:"agent_revision"`
	State         string `json:"state"`
}

// OrgVariables is GET /api/v1/org/list-shared-variables.
type OrgVariables struct {
	Variables []VariableView `json:"variables"`
	Servers   []ServerSync   `json:"servers"`
}

// ServerVariables is GET /api/v1/server/list-shared-variables.
type ServerVariables struct {
	Variables         []VariableView      `json:"variables"`
	Inherited         []InheritedVariable `json:"inherited"`
	BuiltIns          []BuiltinVariable   `json:"builtins"`
	MissingOrgSecrets []string            `json:"missing_org_secrets"`
	Sync              ServerSync          `json:"sync"`
}

// EncryptionKey is one keyed server for the browser fan-out.
type EncryptionKey struct {
	ServerID  string `json:"server_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// PendingServer has never reported a public key, so nothing can be
// encrypted for it yet.
type PendingServer struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
}

// EncryptionKeys is GET /api/v1/org/list-encryption-keys.
type EncryptionKeys struct {
	Servers        []EncryptionKey `json:"servers"`
	PendingServers []PendingServer `json:"pending_servers"`
}

// DispatchedServers lists the servers a resync or reload was sent to.
type DispatchedServers struct {
	Servers []string `json:"servers"`
}

func view(v model.SharedVariable, keyed []string) VariableView {
	out := VariableView{
		ID: v.ID, Name: v.Name, Scope: v.Scope(), IsSecret: v.IsSecret, Value: v.Value,
		Description: v.Description, UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedBy, MissingOn: []string{},
	}
	if v.IsSecret {
		out.Value = command.EncryptedPlaceholder
		out.MissingOn = minus(keyed, v.CipherServers)
	}
	return out
}

// ListOrg returns the org-scope variables and every org server's sync state.
func (u *UseCase) ListOrg(ctx context.Context, orgID string) (OrgVariables, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return OrgVariables{}, err
	}
	vars, err := u.d.Repo.ListOrg(ctx, orgID)
	if err != nil {
		return OrgVariables{}, err
	}
	keyed := keyedServers(scopeServers(keys, nil), pubKeys(keys))
	out := OrgVariables{Variables: make([]VariableView, 0, len(vars)), Servers: make([]ServerSync, 0, len(keys))}
	for _, v := range vars {
		out.Variables = append(out.Variables, view(v, keyed))
	}
	for _, k := range keys {
		info, err := u.d.Repo.ServerVarsInfo(ctx, k.ServerID)
		if err != nil {
			return OrgVariables{}, err
		}
		out.Servers = append(out.Servers, u.syncState(k, info.Revision))
	}
	return out, nil
}

// ListServer returns serverID's own variables, the org's (marked when
// overridden here), the built-ins, the org secrets missing here and the
// server's sync state.
func (u *UseCase) ListServer(ctx context.Context, serverID string) (ServerVariables, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return ServerVariables{}, err
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, info.OrgID)
	if err != nil {
		return ServerVariables{}, err
	}
	key := model.ServerKey{ServerID: serverID, Name: info.Name}
	for _, k := range keys {
		if k.ServerID == serverID {
			key = k
		}
	}
	keyed := []string{}
	if key.PublicKey != "" {
		keyed = []string{serverID}
	}
	own, err := u.d.Repo.ListServer(ctx, serverID)
	if err != nil {
		return ServerVariables{}, err
	}
	org, err := u.d.Repo.ListOrg(ctx, info.OrgID)
	if err != nil {
		return ServerVariables{}, err
	}
	out := ServerVariables{
		Variables: make([]VariableView, 0, len(own)), Inherited: make([]InheritedVariable, 0, len(org)),
		BuiltIns: builtins(info), MissingOrgSecrets: []string{}, Sync: u.syncState(key, info.Revision),
	}
	overridden := map[string]bool{}
	for _, v := range own {
		overridden[v.Name] = true
		out.Variables = append(out.Variables, view(v, keyed))
	}
	for _, v := range org {
		iv := InheritedVariable{VariableView: view(v, keyed), Overridden: overridden[v.Name]}
		out.Inherited = append(out.Inherited, iv)
		if v.IsSecret && len(iv.MissingOn) > 0 && !iv.Overridden {
			out.MissingOrgSecrets = append(out.MissingOrgSecrets, v.Name)
		}
	}
	return out, nil
}

// EncryptionKeys lists the org's keyed servers and the pending ones.
func (u *UseCase) EncryptionKeys(ctx context.Context, orgID string) (EncryptionKeys, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return EncryptionKeys{}, err
	}
	out := EncryptionKeys{Servers: []EncryptionKey{}, PendingServers: []PendingServer{}}
	for _, k := range keys {
		if k.PublicKey == "" {
			out.PendingServers = append(out.PendingServers, PendingServer{ServerID: k.ServerID, Name: k.Name})
			continue
		}
		out.Servers = append(out.Servers, EncryptionKey{ServerID: k.ServerID, Name: k.Name, PublicKey: k.PublicKey})
	}
	return out, nil
}

// syncState compares the agent's last reported revision with the stored one.
func (u *UseCase) syncState(k model.ServerKey, revision int64) ServerSync {
	s := ServerSync{ServerID: k.ServerID, Name: k.Name, HasKey: k.PublicKey != "", Revision: revision, State: SyncPending}
	if u.d.Revisions != nil {
		if rev, ok := u.d.Revisions.VarsRevision(k.ServerID); ok {
			agentRev := rev
			s.AgentRevision = &agentRev
			if rev >= revision {
				s.State = SyncSynced
			}
		}
	}
	return s
}

// builtins lists the agent's read-only WF_ variables with the values the
// API knows.
func builtins(info model.ServerVarsInfo) []BuiltinVariable {
	return []BuiltinVariable{
		{Name: "WF_SERVER_ID", Value: info.ServerID, Description: "This server's id"},
		{Name: "WF_SERVER_NAME", Value: info.Name, Description: "This server's name"},
		{Name: "WF_ORG_ID", Value: info.OrgID, Description: "The organization's id"},
		{Name: "WF_APP_ID", Description: "The app's id (set per app)"},
		{Name: "WF_APP_SLUG", Description: "The app's name as a slug (set per app)"},
		{Name: "WF_APP_DIR", Description: "The app's folder on the server (set per app)"},
		{Name: "WF_DATA_DIR", Description: "The agent's data directory (AGENT_DATA_DIR)"},
		{Name: "WF_TZ", Description: "The server's timezone"},
	}
}
