package agent

import (
	"context"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// InProcessBridge is the standalone-mode analogue of the gRPC Hub: it consumes
// command messages off the (in-process) request queue, runs them through the
// same Dispatcher the agent uses, and publishes the result back onto the
// response queue — all without gRPC or a network hop. This lets the standalone
// binary reuse the exact API -> Bus -> agent -> Bus path the distributed
// topology uses.
type InProcessBridge struct {
	bus        bus.Bus
	cfg        *config.ServerConfig
	dispatcher CommandRunner
	log        *logger.Logger
}

// CommandRunner executes one command envelope and returns its correlated
// reply. *Dispatcher is the production implementation; the API contract
// suite plugs in a scripted fake agent. Same method set as the gRPC agent's
// transport Dispatcher interface.
type CommandRunner interface {
	Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope
}

var _ CommandRunner = (*Dispatcher)(nil)

func NewInProcessBridge(b bus.Bus, cfg *config.ServerConfig, d CommandRunner, log *logger.Logger) *InProcessBridge {
	return &InProcessBridge{bus: b, cfg: cfg, dispatcher: d, log: log}
}

// Start subscribes to the request queue and processes commands until ctx is
// done.
func (br *InProcessBridge) Start(ctx context.Context) error {
	bus.SubscribeJSON(ctx, br.bus, br.cfg.GetBusRequestQueue(), br.log, func(cmd bus.CommandMessage) {
		// A detached command (backup.run) waits for work done elsewhere, and
		// a process.list can wait on a hung /proc read: each gets its own
		// goroutine so the pipeline keeps draining.
		if command.Type(cmd.Type).OffBridge() {
			go br.handle(ctx, cmd)
			return
		}
		br.handle(ctx, cmd)
	})
	br.log.Info("in-process bridge started", "request_queue", br.cfg.GetBusRequestQueue())
	return nil
}

func (br *InProcessBridge) handle(ctx context.Context, cmd bus.CommandMessage) {
	resp := br.dispatcher.Dispatch(ctx, codec.EnvelopeFromCommand(cmd))
	ntf := codec.NotificationFromResponse(cmd.RequestID, resp)
	if err := br.bus.Publish(ctx, br.cfg.GetBusResponseQueue(), ntf); err != nil {
		br.log.Error("inprocess bridge: publish result", "error", err)
	}
}
