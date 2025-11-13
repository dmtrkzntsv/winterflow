package app

import (
	"context"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type UseCase struct {
	appsvc port.AppService
	nm     port.NotificationManager
	log    *logger.Logger
}

type Deps struct {
	AppService          port.AppService
	NotificationManager port.NotificationManager
	Log                 *logger.Logger
}

func NewUseCase(d *Deps) *UseCase {
	return &UseCase{
		appsvc: d.AppService,
		log:    d.Log,
		nm:     d.NotificationManager,
	}
}

func (uc *UseCase) GetApps(ctx context.Context, serverID string) ([]model.App, error) {
	return uc.appsvc.GetApps(ctx, serverID)
}

func (uc *UseCase) CreateApp(ctx context.Context, serverID string, app model.App) error {
	userID := ctx.Value("userID").(string)
	requestID := ctx.Value("requestID").(string)

	return uc.appsvc.CreateApp(ctx, serverID, app, func(app model.App, err error) {
		n := model.Notification{
			Type: model.NotificationOperationResult,
			Ref:  requestID,
		}
		if err != nil {
			n.Status = model.NotificationStatusError
			n.Error = err.Error()
			n.Timestamp = time.Now()
		} else {
			n.Status = model.NotificationStatusSuccess
			n.Payload = app
			n.Timestamp = app.CreatedAt
		}
		// @todo save app to the database
		uc.nm.Publish(userID, n)
	})
}
