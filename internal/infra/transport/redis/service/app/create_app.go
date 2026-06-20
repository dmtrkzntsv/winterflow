package app

import (
	"context"
	"encoding/json"
	"time"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
	"winterflow/pkg/util"
)

// CreateApp publishes an app.save command onto the request queue and waits for
// the agent's reply (relayed back by the Hub onto the response queue). The
// reply id doubles as the envelope's request_id so the Hub can correlate the
// agent's response.
func (s *BusAppService) CreateApp(ctx context.Context, serverID string, app model.App, callback func(app model.App, err error)) error {
	requestID := util.GenerateID()

	cfgBytes, _ := json.Marshal(app)
	req := command.SaveAppRequest{
		App: command.AppPayload{
			AppID:  app.ID,
			Config: cfgBytes,
		},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.log.Error("CreateApp", "error", err)
		callback(model.App{}, err)
		return err
	}

	// Register the reply channel before publishing to avoid racing a fast reply.
	s.rm.CreateReplyChannel(requestID)

	err = s.bus.Publish(ctx, s.cfg.GetBusRequestQueue(), bus.CommandMessage{
		AgentID:   serverID,
		RequestID: requestID,
		Type:      string(command.TypeAppSave),
		Payload:   payload,
	})
	if err != nil {
		s.log.Error("CreateApp", "error", err)
		s.rm.RemoveReplyChannel(requestID)
		callback(model.App{}, err)
		return err
	}

	go func() {
		res, err := s.rm.WaitForReply(requestID, 60*time.Second)
		if err != nil {
			s.log.Error("CreateApp", "error", err)
			callback(model.App{}, err)
			return
		}

		var ntf model.Notification
		if err := json.Unmarshal([]byte(res), &ntf); err != nil {
			s.log.Error("CreateApp", "error", err)
			callback(model.App{}, err)
			return
		}
		if ntf.Status == model.NotificationStatusError {
			callback(model.App{}, errResult(ntf.Error))
			return
		}

		// The notification payload is the agent's SaveAppResponse envelope body.
		var saveResp command.SaveAppResponse
		if raw, ok := ntf.Payload.(json.RawMessage); ok {
			if err := codec.DecodePayload(raw, &saveResp); err != nil {
				s.log.Error("CreateApp: decode response", "error", err)
			}
		} else if ntf.Payload != nil {
			// Payload came back as a decoded interface{}; re-marshal then decode.
			if b, err := json.Marshal(ntf.Payload); err == nil {
				_ = codec.DecodePayload(b, &saveResp)
			}
		}

		result := app
		if saveResp.AppID != "" {
			result.ID = saveResp.AppID
		}
		callback(result, nil)
	}()

	return nil
}

type errResult string

func (e errResult) Error() string { return string(e) }
