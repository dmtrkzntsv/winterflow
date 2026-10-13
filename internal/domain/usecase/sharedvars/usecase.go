// Package sharedvars is the API side of org- and server-level shared
// variables: it validates saves against the browser fan-out coverage rules,
// stores plain values and per-server ciphertexts (never plaintext secrets),
// bumps the affected servers' vars_revision and pushes each one its full
// snapshot as vars.sync. Reloads (reload: true, the reload route) queue or
// extend the org's vars_reload rollout, which reloads the stale running
// apps one at a time once each server reported the change.
package sharedvars

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Per-server sync states as listed by the API; the browser overlays
// "failed" from vars.sync_failed events.
const (
	SyncSynced  = "synced"
	SyncPending = "pending"
)

// agentNotConnected mirrors bus.ErrAgentNotConnected, the hub's answer for
// an agent without a live stream. Such a sync stays pending rather than
// failing: the agent's connect or its next behind-revision report pushes
// again. TestAgentNotConnectedMatchesBus pins the two together.
const agentNotConnected = "agent not connected"

// ErrForbidden reports a server_id (a ciphertexts key, a resync or reload
// target) outside the caller's org.
var ErrForbidden = errors.New("forbidden")

// ValidationError is a refused input (400); Msg is shown to the user.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// ServerDirectory is the slice of port.ServerRepository the usecase needs.
type ServerDirectory interface {
	GetCapability(ctx context.Context, serverID, name string) (string, bool, error)
	GetServerUserIDs(ctx context.Context, serverID string) ([]string, error)
}

// Publisher delivers one SSE notification to a user (the shared
// NotificationManager).
type Publisher interface {
	Publish(userID string, n model.Notification)
}

// RevisionCache remembers each agent's last reported snapshot revision
// (the in-memory status cache).
type RevisionCache interface {
	SetVarsRevision(serverID string, rev int64)
	VarsRevision(serverID string) (int64, bool)
}

// ReloadQueue queues or extends an org's vars_reload rollout
// (*rollouts.UseCase).
type ReloadQueue interface {
	QueueReload(ctx context.Context, userID, orgID string, s model.ReloadScope) error
}

// errNoReloads answers a reload on a usecase built without a queue.
var errNoReloads = errors.New("reloads are not available")

type Deps struct {
	Repo          port.SharedVariableRepository
	Servers       ServerDirectory
	Dispatcher    port.CommandDispatcher
	Notifications Publisher
	Revisions     RevisionCache
	// Reloads is the rollout queue reloads go to (nil: the usecase only
	// syncs, as the event sink's does).
	Reloads ReloadQueue
	Log     *logger.Logger
}

type UseCase struct{ d Deps }

// New builds the usecase. Nil dependencies are allowed for route-table
// documentation, where no method is ever called.
func New(d *Deps) *UseCase { return &UseCase{d: *d} }

// SaveInput is one save. A plain variable always carries Value (required,
// even when only the description changes); a secret carries Ciphertexts
// (server_id -> ECIES for that server), all keyed servers in
// scope for a new value, only servers missing it for a gap fill, or none to
// keep every stored copy (description-only edit).
type SaveInput struct {
	Name        string
	IsSecret    bool
	Value       *string
	Ciphertexts map[string]string
	Description string
	Reload      bool
}

// ResyncInput addresses one server, the servers one variable reaches, or
// (both empty) every org server.
type ResyncInput struct {
	ServerID   string
	VariableID string
}

// ReloadInput is ResyncInput plus the vars.reload filter for a server.
type ReloadInput struct {
	ServerID   string
	VariableID string
	Names      []string
	AppIDs     []string
}

// ServerOrg returns the org that owns serverID.
func (u *UseCase) ServerOrg(ctx context.Context, serverID string) (string, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	return info.OrgID, err
}

// SaveOrg upserts an org-scope variable.
func (u *UseCase) SaveOrg(ctx context.Context, userID, orgID string, in SaveInput) (VariableView, error) {
	return u.save(ctx, userID, orgID, nil, in)
}

// SaveServer upserts a server-scope variable of serverID.
func (u *UseCase) SaveServer(ctx context.Context, userID, serverID string, in SaveInput) (VariableView, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return VariableView{}, err
	}
	id := serverID
	return u.save(ctx, userID, info.OrgID, &id, in)
}

func (u *UseCase) save(ctx context.Context, userID, orgID string, serverID *string, in SaveInput) (VariableView, error) {
	if err := model.ValidateSharedVariableName(in.Name); err != nil {
		return VariableView{}, &ValidationError{Msg: err.Error()}
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return VariableView{}, err
	}
	pub := pubKeys(keys)
	for id := range in.Ciphertexts {
		if _, ok := pub[id]; !ok {
			return VariableView{}, ErrForbidden
		}
	}
	existing, found, err := u.d.Repo.FindByName(ctx, orgID, serverID, in.Name)
	if err != nil {
		return VariableView{}, err
	}
	if found && existing.IsSecret != in.IsSecret {
		return VariableView{}, invalid("%s is a %s variable; delete it and create it again to change that", in.Name, kindOf(existing.IsSecret))
	}
	scope := scopeServers(keys, serverID)
	keyed := keyedServers(scope, pub)
	v := model.SharedVariable{
		OrgID: orgID, ServerID: serverID, Name: in.Name, IsSecret: in.IsSecret, Description: in.Description,
		UpdatedAt: time.Now().UTC().Truncate(time.Second), UpdatedBy: userID, CipherServers: []string{},
	}
	save := model.SharedVariableSave{Create: !found}
	if found {
		v.ID = existing.ID
		v.CipherServers = existing.CipherServers
	} else {
		v.ID = util.GenerateID()
	}

	if !in.IsSecret {
		if len(in.Ciphertexts) > 0 {
			return VariableView{}, invalid("a plain variable takes a value, not ciphertexts")
		}
		// An omitted value is refused rather than read as "": a client that
		// forgets it must never blank the stored value.
		if in.Value == nil {
			return VariableView{}, invalid("a plain variable needs a value")
		}
		v.Value = *in.Value
		if !found || existing.Value != v.Value {
			save.Bump = scope
		}
	} else {
		if in.Value != nil && *in.Value != "" {
			return VariableView{}, invalid("a secret's value travels encrypted in ciphertexts, never as value")
		}
		for id := range in.Ciphertexts {
			if serverID != nil && id != *serverID {
				return VariableView{}, invalid("a server variable is encrypted for its own server only")
			}
			if pub[id] == "" {
				return VariableView{}, invalid("server %s has no public key yet, so it cannot receive secrets", id)
			}
		}
		sent := keysOf(in.Ciphertexts)
		switch {
		case len(sent) == 0 && found:
			// Description-only edit: every stored ciphertext stays.
		case len(sent) == 0:
			if len(keyed) > 0 {
				return VariableView{}, invalid("encrypt the value for every server; missing: %s", strings.Join(keyed, ", "))
			}
			save.Bump = scope // no keyed server yet: agents still learn the name (missing everywhere)
		case found && subsetOf(sent, minus(keyed, existing.CipherServers)):
			// Gap fill: only servers that lack a copy; nothing is overwritten.
			save.Ciphertexts = in.Ciphertexts
			save.Bump = sent
			v.CipherServers = union(existing.CipherServers, sent)
		default:
			if uncovered := minus(keyed, sent); len(uncovered) > 0 {
				return VariableView{}, invalid("encrypt the new value for every server; missing: %s", strings.Join(uncovered, ", "))
			}
			save.Ciphertexts = in.Ciphertexts
			save.ReplaceCiphertexts = true
			save.Bump = scope
			v.CipherServers = sent
		}
	}
	save.Variable = v
	if err := u.d.Repo.Save(ctx, save); err != nil {
		return VariableView{}, err
	}
	u.syncAll(ctx, save.Bump)
	if in.Reload {
		u.queueReload(ctx, userID, orgID, in.Name, save.Bump)
	}
	return view(v, keyed), nil
}

// DeleteOrg removes an org-scope variable of orgID.
func (u *UseCase) DeleteOrg(ctx context.Context, userID, orgID, id string, reload bool) error {
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
		return err
	}
	if v.ServerID != nil {
		return invalid("shared variable not found")
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return err
	}
	return u.remove(ctx, userID, v, scopeServers(keys, nil), reload)
}

// DeleteServer removes a server-scope variable of serverID.
func (u *UseCase) DeleteServer(ctx context.Context, userID, serverID, id string, reload bool) error {
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
		return err
	}
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
		return err
	}
	if v.ServerID == nil || *v.ServerID != serverID {
		return invalid("shared variable not found")
	}
	return u.remove(ctx, userID, v, []string{serverID}, reload)
}

func (u *UseCase) lookup(ctx context.Context, orgID, id string) (model.SharedVariable, error) {
	v, err := u.d.Repo.Get(ctx, id)
	if errors.Is(err, model.ErrSharedVariableNotFound) || (err == nil && v.OrgID != orgID) {
		return model.SharedVariable{}, invalid("shared variable not found")
	}
	return v, err
}

func (u *UseCase) remove(ctx context.Context, userID string, v model.SharedVariable, bump []string, reload bool) error {
	if err := u.d.Repo.Delete(ctx, v.ID, bump); err != nil {
		return err
	}
	u.syncAll(ctx, bump)
	if reload {
		u.queueReload(ctx, userID, v.OrgID, v.Name, bump)
	}
	return nil
}

// queueReload puts a saved or deleted variable's reload on the org's
// rollout, logging (not failing) an error: the change is committed and the
// servers' stale apps can still be reloaded by hand.
func (u *UseCase) queueReload(ctx context.Context, userID, orgID, name string, servers []string) {
	if len(servers) == 0 {
		return
	}
	scope, err := u.reloadScope(ctx, model.ReloadScope{Names: []string{name}, ServerIDs: servers})
	if err == nil {
		err = u.queue(ctx, userID, orgID, scope)
	}
	if err != nil {
		u.d.Log.Warn("queue the reload after a shared variable change", "name", name, "error", err)
	}
}

// reloadScope adds each server's current revision to s: the server's
// report must reach it before its stale apps count (the change travels
// as vars.sync first).
func (u *UseCase) reloadScope(ctx context.Context, s model.ReloadScope) (model.ReloadScope, error) {
	s.Revisions = map[string]int64{}
	for _, id := range s.Servers() {
		info, err := u.d.Repo.ServerVarsInfo(ctx, id)
		if err != nil {
			return model.ReloadScope{}, err
		}
		s.Revisions[id] = info.Revision
	}
	return s, nil
}

func (u *UseCase) queue(ctx context.Context, userID, orgID string, s model.ReloadScope) error {
	if u.d.Reloads == nil {
		return errNoReloads
	}
	return u.d.Reloads.QueueReload(ctx, userID, orgID, s)
}

// Resync pushes the full snapshot to the servers in scope and returns them.
func (u *UseCase) Resync(ctx context.Context, orgID string, in ResyncInput) ([]string, error) {
	targets, _, err := u.targets(ctx, orgID, in.ServerID, in.VariableID)
	if err != nil {
		return nil, err
	}
	for _, id := range targets {
		if err := u.Sync(ctx, id); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// Reload queues (or extends) the org's vars_reload rollout: for one server
// its stale running apps (or those stale for names, or the app ids), for a
// variable the apps stale for its name on every server it reaches. It
// returns the servers in scope.
func (u *UseCase) Reload(ctx context.Context, userID, orgID string, in ReloadInput) ([]string, error) {
	if in.ServerID == "" && in.VariableID == "" {
		return nil, invalid("server_id or variable_id is required")
	}
	targets, name, err := u.targets(ctx, orgID, in.ServerID, in.VariableID)
	if err != nil {
		return nil, err
	}
	s := model.ReloadScope{Names: in.Names, AppIDs: in.AppIDs}
	if name != "" {
		s = model.ReloadScope{Names: []string{name}}
	}
	if err := s.Validate(); err != nil {
		return nil, &ValidationError{Msg: err.Error()}
	}
	switch {
	case len(s.AppIDs) > 0: // the app ids, whatever the names
		s.Names, s.ServerIDs = nil, targets
	case len(s.Names) > 0:
		s.ServerIDs = targets
	default:
		s.AllStale = targets
	}
	if s, err = u.reloadScope(ctx, s); err != nil {
		return nil, err
	}
	if err := u.queue(ctx, userID, orgID, s); err != nil {
		return nil, err
	}
	return targets, nil
}

// targets resolves a resync/reload scope to server ids, plus the
// variable's name when addressed by variable.
func (u *UseCase) targets(ctx context.Context, orgID, serverID, variableID string) ([]string, string, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	all := scopeServers(keys, nil)
	switch {
	case serverID != "":
		for _, id := range all {
			if id == serverID {
				return []string{serverID}, "", nil
			}
		}
		return nil, "", ErrForbidden
	case variableID != "":
		v, err := u.lookup(ctx, orgID, variableID)
		if err != nil {
			return nil, "", err
		}
		if v.ServerID != nil {
			return []string{*v.ServerID}, v.Name, nil
		}
		return all, v.Name, nil
	}
	return all, "", nil
}

// syncAll pushes the snapshot to each server, logging (not failing) a
// publish error: the save is committed and drift or the next connect heals.
func (u *UseCase) syncAll(ctx context.Context, servers []string) {
	for _, id := range servers {
		if err := u.Sync(ctx, id); err != nil {
			u.d.Log.Warn("push shared variables", "server_id", id, "error", err)
		}
	}
}

func kindOf(secret bool) string {
	if secret {
		return "secret"
	}
	return "plain"
}

func pubKeys(keys []model.ServerKey) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k.ServerID] = k.PublicKey
	}
	return out
}

// scopeServers is every org server (org scope) or the one server.
func scopeServers(keys []model.ServerKey, serverID *string) []string {
	if serverID != nil {
		return []string{*serverID}
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.ServerID)
	}
	return out
}

// keyedServers keeps the servers that have a public key.
func keyedServers(scope []string, pub map[string]string) []string {
	out := []string{}
	for _, id := range scope {
		if pub[id] != "" {
			out = append(out, id)
		}
	}
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// minus is a without b, in a's order (never nil).
func minus(a, b []string) []string {
	drop := map[string]bool{}
	for _, s := range b {
		drop[s] = true
	}
	out := []string{}
	for _, s := range a {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

func subsetOf(a, b []string) bool {
	return len(minus(a, b)) == 0
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
