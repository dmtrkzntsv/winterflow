package bootstrap

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/transport/mem/service/reply"
	redisbus "winterflow/internal/infra/transport/redis/bus"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type HubContainer struct {
	factory *HubFactory
}

func (c *HubContainer) GetHubFactory() *HubFactory {
	return c.factory
}

type HubFactory struct {
	bus *redisbus.Bus
	rm  *reply.Manager
	log *logger.Logger
	cfg *config.ServerConfig
}

func BootstrapHUB(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig) *HubContainer {
	addr, pass, db := cfg.GetRedisCredentials()
	rc := redisbus.NewClient(redisbus.Config{
		Addr:     addr,
		Password: pass,
		DB:       db,
	})
	if redisbus.Ping(ctx, rc) != nil {
		log.Fatalf("failed to connect to redis at %s", addr)
	}
	log.Debug("connected to redis", "addr", addr, "db", db)

	b := redisbus.NewBus(rc, log)
	rm := reply.NewReplyManager(log)
	go func() {
		msgs, cancel, err := b.Subscribe(ctx, cfg.GetBusResponseQueue())
		if err != nil {
			log.Fatalf("failed to listen bus: %v", err)
		}
		defer cancel()

		for msg := range msgs {
			log.Debug("received message", "channel", msg.Channel)
			ntf := model.Notification{}
			err := json.Unmarshal([]byte(msg.Payload), &ntf)
			if err != nil {
				log.Error("failed to unmarshal bus message", err)
				continue
			}
			rm.Publish(ntf.Ref, ntf)
		}
	}()

	return &HubContainer{
		factory: &HubFactory{
			bus: b,
			rm:  rm,
			log: log,
			cfg: cfg,
		},
	}
}

func (f *HubFactory) NewUserService() port.UserService {
	//TODO implement me
	panic("implement me")
}

func (f *HubFactory) NewServerRepository() port.ServerRepository {
	//TODO implement me
	panic("implement me")
}

func (f *HubFactory) NewUserRepository() port.UserRepository {
	//TODO implement me
	panic("implement me")
}

func (f *HubFactory) NewAppRepository() port.AppRepository {
	//TODO implement me
	panic("implement me")
}

func (f *HubFactory) NewAppService() port.AppService {
	//TODO implement me
	panic("implement me")
}
