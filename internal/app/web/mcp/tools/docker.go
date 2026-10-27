package tools

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
)

func (t *tools) docker(s *sdk.Server) {
	add(s, "list_networks", "The Docker networks on a server (an app joins an existing one as an external network).",
		readOnly, func(ctx context.Context, in serverInput) *sdk.CallToolResult {
			return t.listDocker(ctx, in, command.TypeNetworkList, command.ListNetworksRequest{}, "list networks")
		})
	add(s, "list_volumes", "The Docker volumes on a server.", readOnly,
		func(ctx context.Context, in serverInput) *sdk.CallToolResult {
			return t.listDocker(ctx, in, command.TypeVolumeList, command.ListVolumesRequest{}, "list volumes")
		})
}

// listDocker dispatches a read-only docker resource list (no usecase
// covers these: the REST handler dispatches them directly).
func (t *tools) listDocker(ctx context.Context, in serverInput, typ command.Type, payload any, what string) *sdk.CallToolResult {
	userID, fail := t.owner(ctx, in.ServerID)
	if fail != nil {
		return fail
	}
	rec, fail := t.dispatch(ctx, userID, in.ServerID, typ, what,
		func(ctx context.Context, o appuc.Observer) (string, error) {
			d := port.DispatchInput{AgentID: in.ServerID, UserID: userID, Type: typ, Payload: payload}
			appuc.Observe(&d, o)
			return t.Dispatcher.Dispatch(ctx, d)
		})
	if fail != nil {
		return fail
	}
	return result(rec)
}
