package agent

import (
	"context"

	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
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
	dispatcher *Dispatcher
	log        *logger.Logger
}

func NewInProcessBridge(b bus.Bus, cfg *config.ServerConfig, d *Dispatcher, log *logger.Logger) *InProcessBridge {
	return &InProcessBridge{bus: b, cfg: cfg, dispatcher: d, log: log}
}

// Start subscribes to the request queue and processes commands until ctx is
// done.
func (br *InProcessBridge) Start(ctx context.Context) error {
	bus.SubscribeJSON(ctx, br.bus, br.cfg.GetBusRequestQueue(), br.log, func(cmd bus.CommandMessage) {
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
