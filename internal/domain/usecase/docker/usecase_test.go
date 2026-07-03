package docker

import (
	"context"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type fakeDispatcher struct {
	inputs []port.DispatchInput
}

func (f *fakeDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	f.inputs = append(f.inputs, in)
	return "rid", nil
}

func TestDockerUseCaseDispatchesTypedCommands(t *testing.T) {
	fd := &fakeDispatcher{}
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	uc := NewUseCase(&Deps{CommandDispatcher: fd, Log: log})
	ctx := context.Background()

	calls := []struct {
		run  func() (string, error)
		typ  command.Type
	}{
		{func() (string, error) { return uc.ListRegistries(ctx, "u", "s") }, command.TypeRegistryList},
		{func() (string, error) {
			return uc.CreateRegistry(ctx, "u", "s", command.CreateRegistryRequest{Address: "a", Username: "n"})
		}, command.TypeRegistryCreate},
		{func() (string, error) { return uc.DeleteRegistry(ctx, "u", "s", "a") }, command.TypeRegistryDelete},
		{func() (string, error) { return uc.ListNetworks(ctx, "u", "s") }, command.TypeNetworkList},
		{func() (string, error) {
			return uc.CreateNetwork(ctx, "u", "s", command.CreateNetworkRequest{Name: "n"})
		}, command.TypeNetworkCreate},
		{func() (string, error) { return uc.DeleteNetwork(ctx, "u", "s", "n") }, command.TypeNetworkDelete},
		{func() (string, error) { return uc.UpdateAgent(ctx, "u", "s", "1.2.3") }, command.TypeAgentUpdate},
	}

	for i, c := range calls {
		rid, err := c.run()
		if err != nil || rid != "rid" {
			t.Fatalf("call %d: rid=%q err=%v", i, rid, err)
		}
		in := fd.inputs[i]
		if in.Type != c.typ || in.UserID != "u" || in.AgentID != "s" {
			t.Fatalf("call %d dispatched %+v, want type %s", i, in, c.typ)
		}
	}
}
