package port

import "winterflow/internal/domain/model"

type NotificationManager interface {
	AddChannel(userID string) chan string
	RemoveChannel(userID string, ch chan string)
	Publish(userID string, n model.Notification)
}
