package templates_test

import (
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
		t.Fatalf("a success keeps the old error: %+v", v)
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
