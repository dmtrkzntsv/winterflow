package reply

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type Manager struct {
	mu      sync.Mutex
	replies map[string]chan string
	log     *logger.Logger
}

func NewReplyManager(log *logger.Logger) *Manager {
	return &Manager{
		replies: make(map[string]chan string),
		log:     log,
	}
}

func (b *Manager) CreateReplyChannel(replyID string) chan string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan string, 1)
	b.replies[replyID] = ch
	return ch
}

func (b *Manager) RemoveReplyChannel(replyID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.replies[replyID]; ok {
		close(ch)
		delete(b.replies, replyID)
	}
}

func (b *Manager) Publish(replyID string, msg model.Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msgBytes, _ := json.Marshal(msg)
	data := string(msgBytes)
	if chn, ok := b.replies[replyID]; ok {
		select {
		case chn <- data:
		default:
		}
	}
}

func (b *Manager) WaitForReply(replyID string, timeout time.Duration) (string, error) {
	b.mu.Lock()
	ch, ok := b.replies[replyID]
	b.mu.Unlock()
	if !ok {
		b.log.Warn("reply channel does not exist")
		return "", fmt.Errorf("no channel found for %s", replyID)
	}
	select {
	case msg := <-ch:
		b.RemoveReplyChannel(replyID)
		return msg, nil
	case <-time.After(timeout):
		b.log.Warn("timeout waiting for reply", "replyID", replyID)
		b.RemoveReplyChannel(replyID)
		return "", fmt.Errorf("timeout waiting for reply %s", replyID)
	}
}
