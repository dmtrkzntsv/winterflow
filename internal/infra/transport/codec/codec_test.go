package codec

import (
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
)

func TestEncodeResponseRoundTrip(t *testing.T) {
	in := command.SaveAppResponse{AppID: "app-1", Revision: "abc123de"}

	env, err := EncodeResponse("agent-1", "req-1", command.TypeAppSave, proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", in)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}
	if env.RequestId != "req-1" {
		t.Errorf("response must echo request id, got %q", env.RequestId)
	}
	if env.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Errorf("response code = %v", env.Base.ResponseCode)
	}

	var out command.SaveAppResponse
	if err := DecodePayload(env.Payload, &out); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if out.AppID != "app-1" || out.Revision != "abc123de" {
		t.Errorf("response payload not preserved: %+v", out)
	}
}

func TestDecodePayloadEmpty(t *testing.T) {
	var out command.GetAppsStatusRequest
	if err := DecodePayload(nil, &out); err != nil {
		t.Errorf("DecodePayload(nil) should be a no-op, got %v", err)
	}
}
