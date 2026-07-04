// Package codec translates between typed command payloads
// (internal/domain/command) and the single gRPC envelope
// (proto.RequestEnvelope / proto.ResponseEnvelope).
//
// The wire format never changes: one envelope carrying a Type string and a JSON
// Payload. The codec is the only place that knows how to (de)serialize that
// payload, so both the API and the agent operate on typed Go values.
package codec

import (
	"encoding/json"
	"fmt"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// EncodeResponse builds a ResponseEnvelope that echoes the originating
// requestID and command type, carrying the JSON encoding of payload.
func EncodeResponse(agentID, requestID string, typ command.Type, code proto.ResponseCode, detail string, payload any) (*proto.ResponseEnvelope, error) {
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("codec: marshal %s response: %w", typ, err)
		}
	}
	return &proto.ResponseEnvelope{
		Base: &proto.BaseResponse{
			MessageId:       requestID,
			Timestamp:       timestamppb.Now(),
			ResponseCode:    code,
			Detail:          detail,
			AgentId:         agentID,
			ProtocolVersion: command.SchemaVersion,
		},
		RequestId:     requestID,
		Type:          string(typ),
		ContentType:   command.ContentTypeJSON,
		SchemaVersion: command.SchemaVersion,
		Payload:       raw,
	}, nil
}

// DecodePayload unmarshals an envelope payload into the typed value v. Use it
// when the expected command type is already known (e.g. a caller awaiting a
// specific response):
//
//	var resp command.SaveAppResponse
//	err := codec.DecodePayload(env.Payload, &resp)
func DecodePayload(raw []byte, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("codec: unmarshal payload: %w", err)
	}
	return nil
}
