package codec

import (
	"bytes"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
)

func TestEncodeRequestRoundTrip(t *testing.T) {
	in := command.SaveAppRequest{
		App: command.AppPayload{
			AppID:  "app-1",
			Config: []byte(`{"name":"demo"}`),
			Variables: []command.ContentItem{
				{ID: "v1", Content: []byte("secret")},
			},
			Files: []command.ContentItem{
				{ID: "f1", Content: []byte("compose")},
			},
		},
	}

	env, err := EncodeRequest("agent-1", "req-1", command.TypeAppSave, in)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if env.Type != string(command.TypeAppSave) {
		t.Errorf("type = %q, want %q", env.Type, command.TypeAppSave)
	}
	if env.RequestId != "req-1" || env.Base.AgentId != "agent-1" {
		t.Errorf("envelope identity not preserved: %+v", env)
	}
	if env.ContentType != command.ContentTypeJSON {
		t.Errorf("content type = %q, want %q", env.ContentType, command.ContentTypeJSON)
	}

	var out command.SaveAppRequest
	if err := DecodePayload(env.Payload, &out); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if out.App.AppID != in.App.AppID {
		t.Errorf("app id = %q, want %q", out.App.AppID, in.App.AppID)
	}
	if !bytes.Equal(out.App.Config, in.App.Config) {
		t.Errorf("config = %q, want %q", out.App.Config, in.App.Config)
	}
	if len(out.App.Variables) != 1 || !bytes.Equal(out.App.Variables[0].Content, []byte("secret")) {
		t.Errorf("variables not preserved: %+v", out.App.Variables)
	}
	if len(out.App.Files) != 1 || !bytes.Equal(out.App.Files[0].Content, []byte("compose")) {
		t.Errorf("files not preserved: %+v", out.App.Files)
	}
}

func TestEncodeResponseRoundTrip(t *testing.T) {
	in := command.SaveAppResponse{AppID: "app-1", Revision: 3}

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
	if out.AppID != "app-1" || out.Revision != 3 {
		t.Errorf("response payload not preserved: %+v", out)
	}
}

func TestNewRequestPayload(t *testing.T) {
	cases := []struct {
		typ command.Type
		ok  bool
	}{
		{command.TypeAppSave, true},
		{command.TypeAppGet, true},
		{command.TypeAppsStatus, true},
		{command.Type("bogus"), false},
	}
	for _, c := range cases {
		v, err := NewRequestPayload(c.typ)
		if c.ok {
			if err != nil || v == nil {
				t.Errorf("NewRequestPayload(%q): want value, got v=%v err=%v", c.typ, v, err)
			}
		} else if err == nil {
			t.Errorf("NewRequestPayload(%q): want error, got nil", c.typ)
		}
	}
}

func TestDecodePayloadEmpty(t *testing.T) {
	var out command.GetAppsStatusRequest
	if err := DecodePayload(nil, &out); err != nil {
		t.Errorf("DecodePayload(nil) should be a no-op, got %v", err)
	}
}
