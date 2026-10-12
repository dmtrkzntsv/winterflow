package sharedvars

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// notifyTimeout bounds the DB work done when an agent's result arrives.
const notifyTimeout = 30 * time.Second

// Sync dispatches serverID's full snapshot as vars.sync (for no user: the
// outcome fans out to the org). After a successful sync, reload names are
// sent as vars.reload.
func (u *UseCase) Sync(ctx context.Context, serverID string, reload []string) error {
	req, err := u.snapshot(ctx, serverID)
	if err != nil {
		return err
	}
	_, err = u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsSync, Payload: req,
		OnResult: func(res port.CommandResult) { u.synced(serverID, reload, res) },
	})
	return err
}

// snapshot builds the server's effective set. The revision is read first,
// so the lists are at least as new as the revision they are sent under;
// only this server's ciphertexts travel, and a secret without one travels
// as a bare name.
func (u *UseCase) snapshot(ctx context.Context, serverID string) (command.SyncVarsRequest, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	org, err := u.d.Repo.ListOrg(ctx, info.OrgID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	own, err := u.d.Repo.ListServer(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	cts, err := u.d.Repo.CiphertextsForServer(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	return command.SyncVarsRequest{
		Revision: info.Revision, ServerID: serverID, ServerName: info.Name, OrgID: info.OrgID,
		Org: entries(org, cts), Server: entries(own, cts),
	}, nil
}

func entries(vs []model.SharedVariable, cts map[string]string) []command.SharedVar {
	out := make([]command.SharedVar, 0, len(vs))
	for _, v := range vs {
		if v.IsSecret {
			out = append(out, command.SharedVar{Name: v.Name, IsSecret: true, Ciphertext: cts[v.ID]})
			continue
		}
		out = append(out, command.SharedVar{Name: v.Name, Value: v.Value})
	}
	return out
}

// synced handles a vars.sync result: a bounce from an unconnected agent
// stays pending silently; any other failure is vars.sync_failed; success
// drops the copies the agent cannot decrypt (only when the result is for
// the current revision: a late result for an older snapshot may name
// ciphertexts replaced since), records the agent's revision (never moving
// the cached one back), announces vars.synced and sends the follow-up
// reload.
func (u *UseCase) synced(serverID string, reload []string, res port.CommandResult) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	if !res.Success {
		if res.Error == agentNotConnected {
			return
		}
		u.notify(ctx, serverID, model.NotificationVarsSyncFailed, model.VarsSyncFailedPayload{ServerID: serverID, Error: res.Error})
		return
	}
	var out command.SyncVarsResponse
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		u.d.Log.Error("vars.sync result: decode", "error", err, "server_id", serverID)
		return
	}
	names := []string{}
	if len(out.Undecryptable) > 0 {
		if info, err := u.d.Repo.ServerVarsInfo(ctx, serverID); err != nil {
			u.d.Log.Warn("drop undecryptable shared secrets", "server_id", serverID, "error", err)
		} else if info.Revision == out.Revision {
			names = u.dropUndecryptable(ctx, serverID, out.Undecryptable)
		}
	}
	if u.d.Revisions != nil {
		if cur, ok := u.d.Revisions.VarsRevision(serverID); !ok || out.Revision > cur {
			u.d.Revisions.SetVarsRevision(serverID, out.Revision)
		}
	}
	u.notify(ctx, serverID, model.NotificationVarsSynced, model.VarsSyncedPayload{ServerID: serverID, Revision: out.Revision, Undecryptable: names})
	if len(reload) > 0 {
		if err := u.dispatchReload(ctx, serverID, command.ReloadVarsRequest{Names: reload}); err != nil {
			u.d.Log.Warn("reload after shared variable change", "server_id", serverID, "error", err)
		}
	}
}

// dropUndecryptable deletes serverID's copies of the secrets its agent
// could not decrypt, so they show as missing there, and returns their names.
func (u *UseCase) dropUndecryptable(ctx context.Context, serverID string, refs []command.VarRef) []string {
	names := []string{}
	if len(refs) == 0 {
		return names
	}
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
		u.d.Log.Warn("drop undecryptable shared secrets", "server_id", serverID, "error", err)
		return names
	}
	var ids []string
	for _, ref := range refs {
		var scope *string
		if ref.Scope == model.SharedScopeServer {
			id := serverID
			scope = &id
		}
		v, ok, err := u.d.Repo.FindByName(ctx, orgID, scope, ref.Name)
		if err != nil || !ok || !v.IsSecret {
			continue
		}
		ids = append(ids, v.ID)
		names = append(names, ref.Name)
	}
	if len(ids) == 0 {
		return names
	}
	if err := u.d.Repo.DropCiphertexts(ctx, serverID, ids); err != nil {
		u.d.Log.Warn("drop undecryptable shared secrets", "server_id", serverID, "error", err)
		return names
	}
	u.d.Log.Warn("dropped shared secrets this server cannot decrypt; enter them again", "server_id", serverID, "names", names)
	return names
}

func (u *UseCase) dispatchReload(ctx context.Context, serverID string, req command.ReloadVarsRequest) error {
	_, err := u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsReload, Payload: req,
		OnResult: func(res port.CommandResult) {
			if res.Success || res.Error == agentNotConnected {
				return
			}
			nctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			names := req.Names
			if names == nil {
				names = []string{}
			}
			u.notify(nctx, serverID, model.NotificationVarsReloaded, command.VarsReloadedPayload{ServerID: serverID, Error: res.Error, Names: names})
		},
	})
	return err
}

// KeyObserved handles a capabilities report before it is stored: a
// public_key different from the stored one invalidates every ciphertext
// encrypted for the old key, so those rows are deleted (the secrets show as
// missing and must be entered again) and the revision is bumped. Pushing
// undecryptable ciphertext is never attempted. It reports whether it
// purged; the caller pushes the new snapshot.
func (u *UseCase) KeyObserved(ctx context.Context, serverID string, caps map[string]string) bool {
	next := caps["public_key"]
	if next == "" || u.d.Servers == nil {
		return false
	}
	prev, ok, err := u.d.Servers.GetCapability(ctx, serverID, "public_key")
	if err != nil || !ok || prev == "" || prev == next {
		return false
	}
	if err := u.d.Repo.PurgeServerCiphertexts(ctx, serverID); err != nil {
		u.d.Log.Error("purge shared secrets after key change", "error", err, "server_id", serverID)
		return false
	}
	u.d.Log.Warn("server key changed: its shared secrets must be entered again", "server_id", serverID)
	return true
}

// ObserveRevision records an agent's reported snapshot revision and says
// whether it is behind servers.vars_revision, returning that revision.
//
// An agent ahead of servers.vars_revision holds a revision this API never
// issued (its database was restored, or the server re-registered). Agents
// ignore snapshots older than the one they hold, so the revision is raised
// past the agent's and the agent counts as behind: the next push replaces
// its snapshot instead of being ignored forever.
func (u *UseCase) ObserveRevision(ctx context.Context, serverID string, agentRev int64) (int64, bool, error) {
	if u.d.Revisions != nil {
		u.d.Revisions.SetVarsRevision(serverID, agentRev)
	}
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return 0, false, err
	}
	if agentRev > info.Revision {
		if err := u.d.Repo.RaiseVarsRevision(ctx, serverID, agentRev+1); err != nil {
			return 0, false, err
		}
		if info, err = u.d.Repo.ServerVarsInfo(ctx, serverID); err != nil {
			return 0, false, err
		}
		u.d.Log.Warn("agent holds a newer shared-variables revision than the database; raised it", "server_id", serverID, "agent_revision", agentRev, "revision", info.Revision)
	}
	return info.Revision, agentRev < info.Revision, nil
}

// Reloaded fans one agent-reported reload outcome out to the org.
func (u *UseCase) Reloaded(ctx context.Context, serverID string, raw []byte) {
	var p command.VarsReloadedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		u.d.Log.Error("vars.reloaded event: decode", "error", err, "server_id", serverID)
		return
	}
	p.ServerID = serverID
	if p.Names == nil {
		p.Names = []string{}
	}
	u.notify(ctx, serverID, model.NotificationVarsReloaded, p)
}

// notify sends one notification to every member of the server's org.
func (u *UseCase) notify(ctx context.Context, serverID string, typ model.NotificationType, payload any) {
	if u.d.Servers == nil || u.d.Notifications == nil {
		return
	}
	users, err := u.d.Servers.GetServerUserIDs(ctx, serverID)
	if err != nil {
		u.d.Log.Debug("shared variables: resolve org members", "server_id", serverID, "error", err)
		return
	}
	n := model.Notification{Type: typ, Payload: payload, Timestamp: time.Now()}
	for _, id := range users {
		u.d.Notifications.Publish(id, n)
	}
}
