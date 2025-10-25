package app

import (
	"context"
	"encoding/json"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/pkg/util"
)

func (s *RedisAppService) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	// @todo
	if err := s.bus.Publish(ctx, "apps:get", nil); err != nil {
		return nil, err
	}

	replyID := util.GenerateID()
	s.rm.CreateReplyChannel(replyID)

	// Wait for matching response or timeout
	msg, err := s.rm.WaitForReply(replyID, 60*time.Second)
	if err != nil {
		s.log.Error("failed to get apps reply", err, "serverID", serverID, "replyID", replyID)
		return nil, err
	}
	// Attempt to unmarshal response
	var resp struct {
		ReplyID string      `json:"reply_id"`
		Apps    []model.App `json:"apps"`
	}
	if err := json.Unmarshal([]byte(msg), &resp); err != nil {
		s.log.Error("failed to unmarshal apps reply", err, "replyID", replyID)
		return nil, err
	}
	return resp.Apps, nil
}
