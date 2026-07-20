package codec

import (
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"
)

func TestEnvelopeFromCommand(t *testing.T) {
	cmd := bus.CommandMessage{
		AgentID:   "agent-1",
		RequestID: "req-1",
		Type:      string(command.TypeAppSave),
		Payload:   []byte(`{"x":1}`),
	}

	env := EnvelopeFromCommand(cmd)

	if env.RequestId != "req-1" || env.Type != string(command.TypeAppSave) {
		t.Fatalf("envelope core fields wrong: %+v", env)
	}
	if string(env.Payload) != `{"x":1}` {
		t.Fatalf("payload = %q", env.Payload)
	}
	if env.ContentType != command.ContentTypeJSON || env.SchemaVersion != command.SchemaVersion {
		t.Fatalf("content/schema constants not applied: %+v", env)
	}
	if env.Base == nil || env.Base.MessageId != "req-1" || env.Base.AgentId != "agent-1" ||
		env.Base.ProtocolVersion != command.SchemaVersion || env.Base.Timestamp == nil {
		t.Fatalf("base message wrong: %+v", env.Base)
	}
}

func TestNotificationFromResponseSuccess(t *testing.T) {
	resp := &proto.ResponseEnvelope{
		Base: &proto.BaseResponse{
			ResponseCode: proto.ResponseCode_RESPONSE_CODE_SUCCESS,
		},
		RequestId: "req-2",
		Payload:   []byte(`{"ok":true}`),
	}

	n := NotificationFromResponse("req-2", resp)

	if n.Type != model.NotificationOperationResult || n.Ref != "req-2" {
		t.Fatalf("notification identity wrong: %+v", n)
	}
	if n.Status != model.NotificationStatusSuccess || n.Error != "" {
		t.Fatalf("expected success, got %+v", n)
	}
	raw, ok := n.Payload.(interface{ MarshalJSON() ([]byte, error) })
	if !ok {
		t.Fatalf("payload should be json.RawMessage, got %T", n.Payload)
	}
	if b, _ := raw.MarshalJSON(); string(b) != `{"ok":true}` {
		t.Fatalf("payload = %s", b)
	}
	if n.Timestamp.IsZero() {
		t.Fatal("timestamp not set")
	}
}

func TestNotificationFromResponseError(t *testing.T) {
	resp := &proto.ResponseEnvelope{
		Base: &proto.BaseResponse{
			ResponseCode: proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR,
			Detail:       "boom",
		},
		RequestId: "req-3",
	}

	n := NotificationFromResponse("req-3", resp)

	if n.Status != model.NotificationStatusError || n.Error != "boom" {
		t.Fatalf("expected error notification, got %+v", n)
	}
	if n.Payload != nil {
		t.Fatalf("error notification must not carry payload, got %v", n.Payload)
	}
}

func TestNotificationFromResponseNilBase(t *testing.T) {
	// A malformed response without Base must not panic and reads as error.
	n := NotificationFromResponse("req-4", &proto.ResponseEnvelope{RequestId: "req-4"})
	if n.Status != model.NotificationStatusError {
		t.Fatalf("nil base should map to error, got %+v", n)
	}
}
