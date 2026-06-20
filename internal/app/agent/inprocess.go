package agent

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"google.golang.org/protobuf/types/known/timestamppb"
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
	msgs, cancel, err := br.bus.Subscribe(ctx, br.cfg.GetBusRequestQueue())
	if err != nil {
		return err
	}
	go func() {
		defer cancel()
		for msg := range msgs {
			var cmd bus.CommandMessage
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				br.log.Error("inprocess bridge: bad command", "error", err)
				continue
			}
			br.handle(ctx, cmd)
		}
	}()
	br.log.Info("in-process bridge started", "request_queue", br.cfg.GetBusRequestQueue())
	return nil
}

func (br *InProcessBridge) handle(ctx context.Context, cmd bus.CommandMessage) {
	req := &proto.RequestEnvelope{
		Base: &proto.BaseMessage{
			MessageId:       cmd.RequestID,
			Timestamp:       timestamppb.Now(),
			AgentId:         cmd.AgentID,
			ProtocolVersion: command.SchemaVersion,
		},
		RequestId:     cmd.RequestID,
		Type:          cmd.Type,
		ContentType:   command.ContentTypeJSON,
		SchemaVersion: command.SchemaVersion,
		Payload:       cmd.Payload,
	}

	resp := br.dispatcher.Dispatch(ctx, req)

	ntf := model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       cmd.RequestID,
		Timestamp: time.Now(),
	}
	if resp.Base != nil && resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		ntf.Status = model.NotificationStatusError
		ntf.Error = resp.Base.Detail
	} else {
		ntf.Status = model.NotificationStatusSuccess
		if len(resp.Payload) > 0 {
			ntf.Payload = json.RawMessage(resp.Payload)
		}
	}
	if err := br.bus.Publish(ctx, br.cfg.GetBusResponseQueue(), ntf); err != nil {
		br.log.Error("inprocess bridge: publish result", "error", err)
	}
}
