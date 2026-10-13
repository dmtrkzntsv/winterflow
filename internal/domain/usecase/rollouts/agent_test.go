package rollouts

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
	"winterflow/pkg/util"
)

// fakeAgent answers app.save and vars.reload on its own goroutine, then
// reports the server's apps twice, healthSettle apart, as the status
// reporter would: the app it touched running (or after[app]) and no longer
// stale.
type fakeAgent struct {
	mu      sync.Mutex
	reports *fakeReports
	apps    map[string]map[string]command.AppStatus // server -> app -> status
	varsRev map[string]int64
	fail    map[string]string                      // app -> error its commands answer
	after   map[string]command.ContainerStatusCode // app -> status after its command
	skip    map[string]bool                        // app -> vars.reload answers skipped
	stuck   map[string]bool                        // app -> stays stale after a reload
	seen    []string                               // "type app" per command
}

func newFakeAgent(reports *fakeReports) *fakeAgent {
	return &fakeAgent{reports: reports, apps: map[string]map[string]command.AppStatus{}, varsRev: map[string]int64{},
		fail: map[string]string{}, after: map[string]command.ContainerStatusCode{}, skip: map[string]bool{}, stuck: map[string]bool{}}
}

func (a *fakeAgent) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	go a.answer(in)
	return util.GenerateID(), nil
}

func (a *fakeAgent) answer(in port.DispatchInput) {
	var (
		appID   string
		payload any
	)
	switch req := in.Payload.(type) {
	case command.SaveAppRequest:
		appID, payload = req.App.AppID, command.SaveAppResponse{AppID: req.App.AppID}
	case command.ReloadVarsRequest:
		appID = req.AppIDs[0]
		payload = command.ReloadVarsResponse{Queued: true, AppID: appID, Skipped: a.skipped(appID)}
	}
	a.mu.Lock()
	a.seen = append(a.seen, string(in.Type)+" "+appID)
	msg := a.fail[appID]
	if msg == "" && !a.skip[appID] {
		code, ok := a.after[appID]
		if !ok {
			code = command.ContainerStatusActive
		}
		st := command.AppStatus{AppID: appID, StatusCode: code, Containers: []command.ContainerStatus{{Name: "web", StatusCode: code}}}
		if a.stuck[appID] {
			st.StaleVars = a.apps[in.AgentID][appID].StaleVars
		}
		a.setLocked(in.AgentID, st)
	}
	a.mu.Unlock()
	res := port.CommandResult{Success: msg == "", Error: msg}
	if msg == "" {
		res.Payload, _ = json.Marshal(payload)
	}
	in.OnResult(res)
	time.Sleep(20 * time.Millisecond) // the next report comes after the answer
	a.report(in.AgentID)
	time.Sleep(40 * time.Millisecond) // and one more, healthSettle (30ms) later
	a.report(in.AgentID)
}

func (a *fakeAgent) skipped(appID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skip[appID]
}

// set records an app's status (stale names included) on a server.
func (a *fakeAgent) set(serverID string, st command.AppStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setLocked(serverID, st)
}

func (a *fakeAgent) setLocked(serverID string, st command.AppStatus) {
	if a.apps[serverID] == nil {
		a.apps[serverID] = map[string]command.AppStatus{}
	}
	a.apps[serverID][st.AppID] = st
}

// report publishes the server's apps to the status cache now.
func (a *fakeAgent) report(serverID string) {
	a.mu.Lock()
	apps := []command.AppStatus{}
	for _, st := range a.apps[serverID] {
		apps = append(apps, st)
	}
	rev := a.varsRev[serverID]
	a.mu.Unlock()
	slices.SortFunc(apps, func(x, y command.AppStatus) int { return compare(x.AppID, y.AppID) })
	a.reports.set(serverID, status.Report{Apps: apps, VarsRevision: rev, HasRevision: true, Received: time.Now()})
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (a *fakeAgent) commands() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.seen)
}

// withAgent wires instances and the app catalog over a fake agent.
func (e *env) withAgent(t *testing.T) (*fakeAgent, *templates.UseCase) {
	t.Helper()
	apps, err := apprepo.New(e.d.Tx, e.d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	a := newFakeAgent(e.reports)
	tu := templates.New(templates.Deps{Repo: e.tpl, Apps: appuc.NewUseCase(&appuc.Deps{CommandDispatcher: a, AppRepository: apps, Log: dbtest.Log}), Log: dbtest.Log})
	e.deps.Instances, e.deps.Apps, e.deps.Dispatcher = tu, apps, a
	e.uc = e.newUseCase()
	return a, tu
}
