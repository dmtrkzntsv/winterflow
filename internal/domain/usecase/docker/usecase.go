// Package docker holds the usecases for Docker resources (registries and
// networks). They are pure agent operations — no DB cache — so every call,
// including the list reads, dispatches to the agent and returns over SSE.
package docker

import (
	"context"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type UseCase struct {
	dispatcher port.CommandDispatcher
	log        *logger.Logger
}

type Deps struct {
	CommandDispatcher port.CommandDispatcher
	Log               *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{dispatcher: d.CommandDispatcher, log: d.Log}
}

func (uc *UseCase) dispatch(ctx context.Context, userID, serverID string, typ command.Type, payload any) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    typ,
		Payload: payload,
	})
}

// --- registries ---

func (uc *UseCase) ListRegistries(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeRegistryList, command.ListRegistriesRequest{})
}

func (uc *UseCase) CreateRegistry(ctx context.Context, userID, serverID string, req command.CreateRegistryRequest) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeRegistryCreate, req)
}

func (uc *UseCase) DeleteRegistry(ctx context.Context, userID, serverID, address string) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeRegistryDelete, command.DeleteRegistryRequest{Address: address})
}

// --- networks ---

func (uc *UseCase) ListNetworks(ctx context.Context, userID, serverID string) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeNetworkList, command.ListNetworksRequest{})
}

func (uc *UseCase) CreateNetwork(ctx context.Context, userID, serverID string, req command.CreateNetworkRequest) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeNetworkCreate, req)
}

func (uc *UseCase) DeleteNetwork(ctx context.Context, userID, serverID, name string) (string, error) {
	return uc.dispatch(ctx, userID, serverID, command.TypeNetworkDelete, command.DeleteNetworkRequest{Name: name})
}
