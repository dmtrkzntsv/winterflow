package app

import (
	"context"
	"encoding/json"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

func (s *RedisAppService) CreateApp(ctx context.Context, serverID string, app model.App, callback func(app model.App, err error)) error {
	replyId := util.GenerateID()
	err := s.bus.Publish(ctx, s.cfg.GetBusRequestQueue(), map[string]any{
		"server_id": serverID,
		"app":       app,
		"reply_id":  replyId,
	})
	if err != nil {
		s.log.Error("CreateApp", "error", err)
		callback(model.App{}, err)
		return err
	}
	s.rm.CreateReplyChannel(replyId)

	go func() {
		res, err := s.rm.WaitForReply(replyId, 60*time.Second)
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

		// @todo fix
		callback(app, nil)
	}()

	return nil
}
