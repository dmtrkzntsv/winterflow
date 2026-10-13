package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	hrollouts "winterflow/internal/app/web/handler/rollouts"
	hsv "winterflow/internal/app/web/handler/sharedvars"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/transport/bus"
)

// reportRunning plays the agent's status reporter until the test ends:
// every 50ms an apps.status of the server with apps() running (and stale()
// as their stale names, when set), at the shared-variables revision rev().
// A rollout step waits for a report received after it.
func reportRunning(t *testing.T, e *env, serverID string, apps func() []string, stale func(appID string) []string, rev func() int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(e.ctx)
	t.Cleanup(cancel)
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			report := command.GetAppsStatusResponse{Apps: []command.AppStatus{}}
			for _, id := range apps() {
				st := command.AppStatus{AppID: id, StatusCode: command.ContainerStatusActive,
					Containers: []command.ContainerStatus{{ContainerID: "c-" + id, Name: "app", StatusCode: command.ContainerStatusActive}}}
				if stale != nil {
					st.StaleVars = stale(id)
				}
				report.Apps = append(report.Apps, st)
			}
			if rev != nil {
				r := rev()
				report.VarsRevision = &r
			}
			body, _ := json.Marshal(report)
			_ = e.bus.Publish(ctx, e.cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: serverID, Kind: bus.EventAppsStatus, Payload: body})
		}
	}()
}

// fixed is a constant app list for reportRunning.
func fixed(ids ...string) func() []string { return func() []string { return ids } }

// awaitRollout waits for the rollout event of id in state.
func awaitRollout(t *testing.T, c *client, id, state string) model.RolloutView {
	t.Helper()
	var v model.RolloutView
	awaitEvent(t, c, "rollout "+id+" "+state, func(ev event) bool {
		var got model.RolloutView
		if ev.Type != model.NotificationRollout || json.Unmarshal(ev.Payload, &got) != nil || got.ID != id || got.State != state {
			return false
		}
		v = got
		return true
	})
	return v
}

// rolloutsOf reads get-rollouts.
func rolloutsOf(t *testing.T, c *client, q url.Values) []model.RolloutView {
	t.Helper()
	var list hrollouts.RolloutList
	c.get(t, "/api/v1/rollout/get-rollouts", q).expect(t, http.StatusOK).data(t, &list)
	return list.Rollouts
}

func TestUpgradeAllRollout(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
	reportRunning(t, e, sid, fixed(a, b), nil, nil)
	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})

	var queued model.RolloutView
	owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusOK).data(t, &queued)
	if queued.State != "queued" || queued.Kind != model.RolloutTemplateUpgrade || queued.ScopeKey != ie.tplID || queued.Revision != 2 {
		t.Fatalf("queued = %+v", queued)
	}
	t.Run("one upgrade of a template at a time", func(t *testing.T) {
		owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID, Revision: 2}).expect(t, http.StatusConflict)
	})
	done := awaitRollout(t, owner, queued.ID, "done")
	if done.Done != 2 || done.Skipped != 0 || done.Total != 2 || done.FinishedAt == nil {
		t.Fatalf("done = %+v", done)
	}
	t.Run("every instance is on the new revision", func(t *testing.T) {
		for _, key := range []string{"tenant-1", "tenant-2"} {
			if v := instanceOf(t, owner, sid, key); v.Revision != 2 || v.State != "created" {
				t.Fatalf("%s = %+v", key, v)
			}
		}
	})
	t.Run("get-rollouts lists it", func(t *testing.T) {
		list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {ie.tplID}})
		if len(list) != 1 || list[0].ID != queued.ID || list[0].State != "done" {
			t.Fatalf("list = %+v", list)
		}
		owner.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {"bogus"}}).expect(t, http.StatusBadRequest)
	})
	t.Run("another org's template and rollout are unknown", func(t *testing.T) {
		mallory, _ := e.foreignClient(t)
		mallory.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusNotFound)
		mallory.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {ie.tplID}}).
			expect(t, http.StatusNotFound)
		mallory.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusNotFound)
		if list := rolloutsOf(t, mallory, url.Values{"kind": {model.RolloutTemplateUpgrade}}); len(list) != 0 {
			t.Fatalf("mallory sees %+v", list)
		}
	})
}

func TestStopRollout(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
	reportRunning(t, e, sid, fixed(a, b), nil, nil)
	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})

	// The first upgrade's save is held until Stop was asked.
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	e.agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		<-release
		return reply{Payload: command.SaveAppResponse{AppID: decode[command.SaveAppRequest](raw).App.AppID, Revision: "rev-2"}}
	})
	var queued model.RolloutView
	owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusOK).data(t, &queued)
	awaitEvent(t, owner, "the first step", func(ev event) bool {
		var v model.RolloutView
		return ev.Type == model.NotificationRollout && json.Unmarshal(ev.Payload, &v) == nil && v.ID == queued.ID && v.CurrentAppID == a
	})
	var stopping model.RolloutView
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusOK).data(t, &stopping)
	if !stopping.StopRequested || stopping.State != "running" {
		t.Fatalf("stopping = %+v", stopping)
	}
	free()
	stopped := awaitRollout(t, owner, queued.ID, "stopped")
	if stopped.Done != 1 || stopped.CurrentAppID != "" {
		t.Fatalf("stopped = %+v", stopped)
	}
	if v := instanceOf(t, owner, sid, "tenant-2"); v.Revision != 1 {
		t.Fatalf("tenant-2 = %+v: nothing follows a stop", v)
	}
	// Stopping an ended rollout answers it as it is; a malformed id is 400.
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: "nope"}).expect(t, http.StatusBadRequest)
}

func TestVarsReloadRollout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	// The agent reports anApp running and stale for SMTP_HOST until it got
	// the reload, at the server's current revision (0: nothing saved).
	reloaded := func() bool {
		for _, c := range e.agent.seen(command.TypeVarsReload) {
			if in := decode[command.ReloadVarsRequest](c.Payload); len(in.AppIDs) == 1 && in.AppIDs[0] == anApp {
				return true
			}
		}
		return false
	}
	stale := func(string) []string {
		if reloaded() {
			return nil
		}
		return []string{"SMTP_HOST"}
	}
	reportRunning(t, e, sid, fixed(anApp), stale, func() int64 { return 0 })

	var d sharedvars.DispatchedServers
	owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sid}).expect(t, http.StatusOK).data(t, &d)
	if len(d.Servers) != 1 || d.Servers[0] != sid {
		t.Fatalf("reload = %+v", d)
	}
	list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
	if len(list) != 1 {
		t.Fatalf("rollouts = %+v", list)
	}
	done := awaitRollout(t, owner, list[0].ID, "done")
	if done.Done != 1 || done.Total != 1 || done.Kind != model.RolloutVarsReload {
		t.Fatalf("done = %+v", done)
	}
	if got := e.agent.seen(command.TypeVarsReload); len(got) != 1 || got[0].AgentID != sid {
		t.Fatalf("vars.reload sent = %+v", got)
	}
	// The org's id is its scope; another org's is unknown.
	owner.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutVarsReload}, "scope_key": {done.ScopeKey}}).expect(t, http.StatusOK)
	mallory, _ := e.foreignClient(t)
	mallory.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutVarsReload}, "scope_key": {done.ScopeKey}}).
		expect(t, http.StatusNotFound)
}

func TestVarsReloadRolloutStopsAtAFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	e.agent.fail(command.TypeVarsReload, "reload after a change to SMTP_HOST failed: docker compose up: exit status 1")
	// The app is in the catalog: a failed reload of an app gone from it
	// would count as skipped (deleted meanwhile).
	if err := e.deps.AppRepository.SaveApp(e.dbCtx(t), model.App{ID: anApp, ServerID: sid, Name: "shop"}); err != nil {
		t.Fatal(err)
	}
	reportRunning(t, e, sid, fixed(anApp), func(string) []string { return []string{"SMTP_HOST"} }, func() int64 { return 0 })
	owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sid, Names: []string{"SMTP_HOST"}}).expect(t, http.StatusOK)
	list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
	failed := awaitRollout(t, owner, list[0].ID, "failed")
	if failed.CurrentAppID != anApp || failed.CurrentServerID != sid || failed.Error != "reload after a change to SMTP_HOST failed: docker compose up: exit status 1" {
		t.Fatalf("failed = %+v", failed)
	}
}
