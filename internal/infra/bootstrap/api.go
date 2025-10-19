package bootstrap

import (
	"context"
	"encoding/json"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	infrafs "winterflow/internal/infra/agent/repository"
	infradb "winterflow/internal/infra/db/repository"
	"winterflow/internal/infra/mem/service/reply"
	redisbus "winterflow/internal/infra/redis/bus"
	redisappsrv "winterflow/internal/infra/redis/service/app"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type ApiFactory struct {
	bus *redisbus.Bus
	rm  *reply.Manager
	log *logger.Logger
	cfg *config.Config
}

func NewApiFactory(ctx context.Context, log *logger.Logger, cfg *config.Config) *ApiFactory {
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

	return &ApiFactory{
		bus: b,
		rm:  rm,
		log: log,
		cfg: cfg,
	}
}

func (sf *ApiFactory) NewServerRepository() port.ServerRepository {
	return infradb.NewDbServerRepository()
}

func (sf *ApiFactory) NewAppRepository() port.AppRepository {
	return infrafs.NewFsAppRepository()
}

func (sf *ApiFactory) NewAppService() port.AppService {
	return redisappsrv.NewAppService(sf.log, sf.cfg, sf.bus, sf.rm)
}
