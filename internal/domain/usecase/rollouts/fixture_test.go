package rollouts

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	members    = []string{"u1", "u2"}
)

// fakeMembers: every org has members u1 and u2.
type fakeMembers struct{}

func (fakeMembers) GetOrgUserIDs(context.Context, string) ([]string, error) {
	return slices.Clone(members), nil
}

// fakePublisher records rollout events (u1's copy of each).
type fakePublisher struct {
	mu   sync.Mutex
	seen []model.RolloutView
	n    int
}

func (f *fakePublisher) Publish(userID string, n model.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if v, ok := n.Payload.(model.RolloutView); ok && n.Type == model.NotificationRollout && userID == "u1" {
		f.seen = append(f.seen, v)
	}
}

func (f *fakePublisher) events() []model.RolloutView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// fakeReports is a settable status cache.
type fakeReports struct {
	mu      sync.Mutex
	reports map[string]status.Report
	reads   int
}

func (f *fakeReports) set(serverID string, rep status.Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports[serverID] = rep
}

// setRead sets rep and waits until it has been read (false when stop
// closes first), so a sequence of reports reaches a poller one by one.
func (f *fakeReports) setRead(serverID string, rep status.Report, stop <-chan struct{}) bool {
	f.mu.Lock()
	f.reports[serverID] = rep
	n := f.reads
	f.mu.Unlock()
	for {
		f.mu.Lock()
		read := f.reads > n
		f.mu.Unlock()
		if read {
			return true
		}
		select {
		case <-stop:
			return false
		case <-time.After(time.Millisecond):
		}
	}
}

func (f *fakeReports) LatestReport(serverID string, _ time.Time) (status.Report, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	rep, ok := f.reports[serverID]
	return rep, ok
}

// env is the usecase over a real database with fakes around it and short
// bounds.
type env struct {
	uc      *UseCase
	deps    Deps
	d       *db.DB
	repo    *rolloutrepo.Repo
	tpl     *templaterepo.Repo
	pub     *fakePublisher
	reports *fakeReports
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	repo, err := rolloutrepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{d: d, repo: repo, tpl: tpl, pub: &fakePublisher{}, reports: &fakeReports{reports: map[string]status.Report{}}}
	e.deps = Deps{Repo: repo, Templates: tpl, Status: e.reports, Members: fakeMembers{}, Notifications: e.pub, Log: dbtest.Log, Timing: testTiming}
	e.uc = e.newUseCase()
	return e
}

// testTiming is the fixtures' short bounds. Times are stored to the
// second: a park shorter than that would be over at once, so the poll is
// just above one.
var testTiming = Timing{
	Renew: 10 * time.Millisecond, Idle: time.Hour, SettlePoll: 1100 * time.Millisecond, HealthPoll: 5 * time.Millisecond,
	Health: 200 * time.Millisecond, HealthSettle: 30 * time.Millisecond, Settle: 200 * time.Millisecond,
}

// newUseCase is another API process on the same database.
func (e *env) newUseCase() *UseCase { return New(e.deps) }

// get reads a rollout.
func (e *env) get(t *testing.T, id string) model.Rollout {
	t.Helper()
	r, err := e.repo.GetRollout(dbtest.Ctx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// eventually polls cond for up to 5 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 5s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
