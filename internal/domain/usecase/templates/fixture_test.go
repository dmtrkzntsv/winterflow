package templates_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
	templaterepo "winterflow/internal/infra/db/repository/template"
	"winterflow/pkg/crypto/cryptotest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
)

// dispatcher records every command; tests answer the last one with reply.
type dispatcher struct {
	mu   sync.Mutex
	sent []port.DispatchInput
}

func (d *dispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, in)
	return "req-" + strconv.Itoa(len(d.sent)), nil
}

func (d *dispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func (d *dispatcher) last(t *testing.T) port.DispatchInput {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sent) == 0 {
		t.Fatal("nothing dispatched")
	}
	return d.sent[len(d.sent)-1]
}

// reply runs the last command's result hook as the agent's answer would.
func (d *dispatcher) reply(t *testing.T, res port.CommandResult) {
	t.Helper()
	in := d.last(t)
	if res.Success && len(res.Payload) == 0 && in.Type == command.TypeAppSave {
		res.Payload, _ = json.Marshal(command.SaveAppResponse{AppID: in.Payload.(command.SaveAppRequest).App.AppID})
	}
	in.OnResult(res)
}

// noStorages is a storage index without any storage.
type noStorages struct{}

func (noStorages) ListForServer(context.Context, string) ([]model.StorageSummary, error) {
	return nil, nil
}
func (noStorages) Upsert(context.Context, string, model.StorageSummary) error { return nil }
func (noStorages) Delete(context.Context, string, string) error               { return nil }
func (noStorages) ReplaceForServer(context.Context, string, []model.StorageSummary) error {
	return nil
}

type fixture struct {
	uc   *templates.UseCase
	disp *dispatcher
	tpl  templates.SavedTemplate
	// secret is the KEY ciphertext create sends.
	secret string
}

const compose = `services:
  app:
    image: example/shop:${VERSION}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

func strp(s string) *string { return &s }

func body() template.Body {
	return template.Body{
		Files: map[string]string{"compose.yaml": compose},
		Parameters: []template.Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]+$"},
			{Name: "VERSION", Default: strp("1")},
			{Name: "KEY", Secret: true, Required: true},
		},
		AppName: "shop-${NAME}",
		Backups: map[string]template.BackupDefault{"db": {Enabled: false}},
	}
}

// newFixture is the usecase over a real database (org1 owns srv1, org2
// srv2) and a recording dispatcher, with template "shop" saved in org1.
// storages, when set, is the server storage index save-app checks.
func newFixture(t *testing.T, storages port.ServerStorageRepository) fixture {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	repo, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := apprepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	disp := &dispatcher{}
	appDeps := &appuc.Deps{CommandDispatcher: disp, AppRepository: apps, Log: dbtest.Log}
	if storages != nil {
		appDeps.ServerStorageRepository = storages
	}
	uc := templates.New(templates.Deps{Repo: repo, Apps: appuc.NewUseCase(appDeps), Log: dbtest.Log})
	tpl, err := uc.SaveTemplate(dbtest.Ctx(t), "u1", org1, templates.SaveInput{Name: "shop", Body: body()})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{uc: uc, disp: disp, tpl: tpl, secret: cryptotest.Ciphertext(t, "key")}
}
