package codec

import (
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// EnvelopeFromCommand projects a bus-level command onto the gRPC request
// envelope. Both bridges (the distributed Hub and the standalone in-process
// bridge) use this so the wire constants are applied in exactly one place.
func EnvelopeFromCommand(cmd bus.CommandMessage) *proto.RequestEnvelope {
	return &proto.RequestEnvelope{
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
}

// NotificationFromResponse maps an agent's response envelope to the
// model.Notification published on the response queue. A missing Base is
// treated as an error (malformed reply) rather than a silent success.
func NotificationFromResponse(requestID string, resp *proto.ResponseEnvelope) model.Notification {
	n := model.Notification{
		Type:      model.NotificationOperationResult,
		Ref:       requestID,
		Timestamp: time.Now(),
	}
	if resp.Base == nil {
		n.Status = model.NotificationStatusError
		n.Error = "malformed agent response: missing base"
		return n
	}
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		n.Status = model.NotificationStatusError
		n.Error = resp.Base.Detail
		return n
	}
	n.Status = model.NotificationStatusSuccess
	if len(resp.Payload) > 0 {
		n.Payload = json.RawMessage(resp.Payload)
	}
	return n
}
