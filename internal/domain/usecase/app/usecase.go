package app

import (
	"context"
	"time"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// ctxKey is a private type for context keys so they can't collide with keys
// defined in other packages.
type ctxKey string

const (
	ctxUserID    ctxKey = "userID"
	ctxRequestID ctxKey = "requestID"
)

// WithUserID and WithRequestID attach the routing identifiers the CreateApp
// async callback needs. Handlers call these before invoking the usecase.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxUserID, userID)
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxRequestID, requestID)
}

func userIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

func requestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}

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
	userID := userIDFrom(ctx)
	requestID := requestIDFrom(ctx)

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
		// @todo persist the app to the database once the apps repository write
		// path is ported (out of scope for the first vertical slice).
		uc.nm.Publish(userID, n)
	})
}
