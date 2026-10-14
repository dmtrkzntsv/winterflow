package rollouts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/util"
)

// extendTries bounds how often QueueReload retries a lost race (another
// request extended the scope, or the rollout ended, in between).
const extendTries = 5

// QueueReload queues the org's vars_reload rollout for s, or extends the
// active one: its scope becomes the union, written in one conditional
// update (the next app is always chosen from live staleness, so a running
// rollout can take more). A rollout with a stop requested takes nothing
// (model.ErrRolloutStopping): it would end stopped and drop the reload.
// SettleBy is set here: servers get settleWait to
// report the change.
func (uc *UseCase) QueueReload(ctx context.Context, userID, orgID string, s model.ReloadScope) error {
	s.SettleBy = uc.now().Add(uc.t.Settle).UTC()
	for range extendTries {
		active, err := uc.d.Repo.GetActiveRollout(ctx, model.RolloutVarsReload, orgID)
		if errors.Is(err, model.ErrRolloutNotFound) {
			raw, err := json.Marshal(s)
			if err != nil {
				return err
			}
			_, ok, err := uc.queue(ctx, model.Rollout{
				ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutVarsReload, Scope: raw, ScopeKey: orgID, CreatedBy: userID,
			})
			if err != nil || ok {
				return err
			}
			continue // queued by another request meanwhile: extend that one
		}
		if err != nil {
			return err
		}
		if active.StopRequested {
			// It will end stopped: an extension would be dropped with it.
			return model.ErrRolloutStopping
		}
		cur, err := reloadScope(active)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(cur.Merge(s))
		if err != nil {
			return err
		}
		ok, err := uc.d.Repo.ExtendRollout(ctx, active.ID, active.ScopeRev, raw)
		if err != nil {
			return err
		}
		if ok {
			uc.Wake()
			return nil
		}
	}
	return errors.New("queue the reload: the reload rollout kept changing; try again")
}

// reloader is the vars_reload kind.
type reloader struct{ uc *UseCase }

func reloadScope(r model.Rollout) (model.ReloadScope, error) {
	var s model.ReloadScope
	if err := json.Unmarshal(r.Scope, &s); err != nil {
		return model.ReloadScope{}, errBadScope
	}
	return s, nil
}

// candidate is a stale running app a vars_reload may take next.
type candidate struct {
	serverID, appID string
	rev             int64
}

// next is the first stale running app the scope wants, by server and app
// id, from the status cache. A server whose latest report predates the
// change (its revision is behind the scope's) does not count yet: until
// SettleBy the rollout waits for it, then leaves it out.
func (k reloader) next(_ context.Context, r model.Rollout) (pick, error) {
	s, err := reloadScope(r)
	if err != nil {
		return pick{}, err
	}
	now := k.uc.now()
	waiting := false
	var todo []candidate
	for _, serverID := range s.Servers() {
		rep, ok := k.uc.d.Status.LatestReport(serverID, now)
		if !ok || (rep.HasRevision && rep.VarsRevision < s.Revisions[serverID]) {
			waiting = waiting || now.Before(s.SettleBy)
			continue
		}
		for _, a := range rep.Apps {
			if !slices.Contains(r.SkipIDs, a.AppID) && running(a) && s.Wants(serverID, a.AppID, a.StaleVars) {
				todo = append(todo, candidate{serverID: serverID, appID: a.AppID, rev: rep.VarsRevision})
			}
		}
	}
	if len(todo) == 0 {
		return pick{wait: waiting}, nil
	}
	slices.SortFunc(todo, func(a, b candidate) int {
		return strings.Compare(a.serverID+"/"+a.appID, b.serverID+"/"+b.appID)
	})
	c := todo[0]
	return pick{appID: c.appID, serverID: c.serverID, skipID: c.appID, remaining: int64(len(todo)), revSeen: c.rev}, nil
}

// run sends the single-app vars.reload and waits for its outcome (the
// agent answers when the reload ended) and for the app to run. An app the
// agent found stopped is skipped, and so is one deleted while its reload
// was queued or in flight (the step failed and the app is gone from the
// catalog). One still stale for the scope at the same snapshot afterwards
// fails the step instead of being picked again.
func (k reloader) run(ctx context.Context, r model.Rollout, p pick) stepResult {
	res := k.reload(ctx, r, p)
	if res.outcome == stepFailed && ctx.Err() == nil && k.deleted(ctx, p.appID) {
		return skipped()
	}
	return res
}

// deleted reports whether appID is gone from the API's app catalog.
func (k reloader) deleted(ctx context.Context, appID string) bool {
	if k.uc.d.Apps == nil {
		return false
	}
	_, err := k.uc.d.Apps.GetApp(ctx, appID)
	return errors.Is(err, model.ErrAppNotFound)
}

func (k reloader) reload(ctx context.Context, r model.Rollout, p pick) stepResult {
	results, deliver := port.NewResultChan()
	if _, err := k.uc.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: p.serverID, UserID: r.CreatedBy, Type: command.TypeVarsReload,
		Payload: command.ReloadVarsRequest{AppIDs: []string{p.appID}}, Wait: k.uc.t.Command,
		OnResult: deliver,
	}); err != nil {
		return failed(err.Error())
	}
	res, err := k.uc.awaitResult(ctx, results)
	if err != nil {
		return failed(err.Error())
	}
	var out command.ReloadVarsResponse
	if json.Unmarshal(res.Payload, &out) == nil && out.Skipped {
		return skipped()
	}
	st, err := k.uc.awaitRunning(ctx, p.serverID, p.appID, k.uc.now())
	if err != nil {
		return failed(err.Error())
	}
	s, err := reloadScope(r)
	if err != nil {
		return failed(err.Error())
	}
	if s.Wants(p.serverID, p.appID, st.StaleVars) {
		if rep, ok := k.uc.d.Status.LatestReport(p.serverID, k.uc.now()); ok && rep.VarsRevision == p.revSeen {
			return failed("the app is still stale after the reload: " + strings.Join(st.StaleVars, ", "))
		}
	}
	return done()
}

// running reports whether one of the app's containers runs.
func running(a command.AppStatus) bool {
	for _, c := range a.Containers {
		if c.StatusCode == command.ContainerStatusActive {
			return true
		}
	}
	return false
}
