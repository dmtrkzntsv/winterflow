package templates_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

func TestControlInstance(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	if _, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlUpdate); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("update action: %v", err)
	}
	out, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStop)
	if err != nil || f.disp.last(t).Type != command.TypeAppControl {
		t.Fatalf("control = %+v, %v", out, err)
	}
	f.disp.reply(t, port.CommandResult{Error: "stop failed"})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.LastError != "stop failed" || v.State != "created" {
		t.Fatalf("after a failed stop = %+v", v)
	}
	out, _ = f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStart)
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.LastError != "" {
		t.Fatalf("a success did not clear the old error: %+v", v)
	}
}

func TestDeleteRetiresAndUnlinkFrees(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotCreated) {
		t.Fatalf("unlink of a pending instance: %v", err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})

	un, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1")
	if err != nil {
		t.Fatal(err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	if strings.Contains(string(req.App.Config), `"instance"`) || strings.Contains(string(req.App.Config), `"template_id"`) {
		t.Fatalf("unlink config = %s", req.App.Config)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, un.Done)
	if v, err := un.Final(ctx); err != nil || v.State != "unlinked" {
		t.Fatalf("unlinked = %+v, %v", v, err)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("after unlink: %v", err)
	}

	created(t, f) // the freed key makes a new instance
	del, err := f.uc.DeleteInstance(ctx, "u1", org1, srv1, "k1", true, false)
	if err != nil || f.disp.last(t).Type != command.TypeAppDelete {
		t.Fatalf("delete = %+v, %v", del, err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, del.Done)
	if v, _ := del.Final(ctx); v.State != "deleted" {
		t.Fatalf("deleted = %+v", v)
	}
	n := f.disp.count()
	again, err := f.uc.DeleteInstance(ctx, "u1", org1, srv1, "k1", true, false)
	if err != nil || again.Instance == nil || again.Instance.State != "deleted" || f.disp.count() != n {
		t.Fatalf("delete again = %+v, %v", again, err)
	}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("a retired key: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1"}); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("update of a deleted instance: %v", err)
	}
	if _, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStart); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("control of a deleted instance: %v", err)
	}
}

// A failed delete leaves the instance created with the agent's error: the
// key is not retired and a replayed create still answers the instance.
func TestDeleteFailureKeepsTheInstance(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	del, err := f.uc.DeleteInstance(ctx, "u1", org1, srv1, "k1", false, false)
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Error: "compose down failed"})
	if res := received(t, del.Done); res.Success {
		t.Fatal("a failure was delivered as a success")
	}
	got, err := f.uc.GetInstance(ctx, org1, srv1, "k1")
	if err != nil || got.State != "created" || got.LastError != "compose down failed" {
		t.Fatalf("after a failed delete = %+v, %v", got, err)
	}
	n := f.disp.count()
	replay, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	if err != nil || replay.Instance == nil || replay.Instance.State != "created" || f.disp.count() != n {
		t.Fatalf("a replay after a failed delete = %+v, %v", replay, err)
	}
}

// A failed unlink leaves the instance created: its key is not freed.
func TestUnlinkFailureKeepsTheKey(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	un, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1")
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Error: "commit failed"})
	received(t, un.Done)
	got, err := f.uc.GetInstance(ctx, org1, srv1, "k1")
	if err != nil || got.State != "created" || got.LastError != "commit failed" {
		t.Fatalf("after a failed unlink = %+v, %v", got, err)
	}
	other := f.create(map[string]string{"NAME": "other"})
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, other); !errors.Is(err, model.ErrKeyInUse) {
		t.Fatalf("the key after a failed unlink: %v", err)
	}
}

// Unlink saves the app as it is: the agent keeps its secrets (sent as the
// keep placeholder) and its backups (no list in the config). Its result is
// read back from the database.
func TestUnlinkKeepsSecretsAndBackups(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	stop, _ := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStop)
	f.disp.reply(t, port.CommandResult{Error: "stop failed"})
	received(t, stop.Done)

	un, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1")
	if err != nil {
		t.Fatal(err)
	}
	vars, _ := saved(t, f)
	if k := vars["KEY"]; string(k.Content) != command.EncryptedPlaceholder || !k.Encrypted {
		t.Fatalf("unlink sent the secret as %+v", k)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(req.App.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["backups"]; ok {
		t.Fatalf("unlink replaced the backups list: %s", req.App.Config)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, un.Done)
	v, err := un.Final(ctx)
	if err != nil || v.State != "unlinked" || v.LastError != "" {
		t.Fatalf("unlinked = %+v, %v", v, err)
	}
}
