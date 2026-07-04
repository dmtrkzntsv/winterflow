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

func TestSourcePayloadRoundTrip(t *testing.T) {
	in := command.SaveAppRequest{App: command.AppPayload{
		AppID:  "app-1",
		Config: []byte(`{}`),
		Source: &command.SourcePayload{
			RepoURL:     "https://github.com/org/app",
			Branch:      "main",
			ComposePath: "deploy/compose.yml",
			AutoUpdate:  true,
			PollSeconds: 60,
			Token:       []byte("cipher"),
		},
	}}
	env, err := EncodeResponse("a", "r", command.TypeAppSave, proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", in)
	if err != nil {
		t.Fatal(err)
	}
	var out command.SaveAppRequest
	if err := DecodePayload(env.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.App.Source == nil || out.App.Source.RepoURL != in.App.Source.RepoURL ||
		out.App.Source.ComposePath != "deploy/compose.yml" || !out.App.Source.AutoUpdate ||
		string(out.App.Source.Token) != "cipher" {
		t.Fatalf("source not preserved: %+v", out.App.Source)
	}

	// Absent source stays absent (omitempty).
	env2, _ := EncodeResponse("a", "r", command.TypeAppSave, proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.SaveAppRequest{App: command.AppPayload{AppID: "x", Config: []byte(`{}`)}})
	var out2 command.SaveAppRequest
	_ = DecodePayload(env2.Payload, &out2)
	if out2.App.Source != nil {
		t.Fatal("nil source should stay nil")
	}
}

func TestImageTagsRoundTrip(t *testing.T) {
	env, err := EncodeResponse("a", "r", command.TypeImageTags, proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok",
		command.ImageTagsResponse{Image: "nginx", Tags: []string{"latest", "1.27"}})
	if err != nil {
		t.Fatal(err)
	}
	var out command.ImageTagsResponse
	if err := DecodePayload(env.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.Image != "nginx" || len(out.Tags) != 2 {
		t.Fatalf("round-trip = %+v", out)
	}
}
