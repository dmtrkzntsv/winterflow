package bootstrap

import (
	"context"
	agentsrv "winterflow/internal/infra/agent/service"
	"winterflow/internal/infra/bootstrap/container"
	"winterflow/internal/infra/cert"
	"winterflow/internal/infra/db"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func BootstrapStandalone(log *logger.Logger, cfg *config.ServerConfig) *container.StandaloneContainer {
	dbconn := db.NewBunConnection(log, cfg.GetDbURL())
	agentservice := agentsrv.NewAgentService()
	certmanager, err := cert.NewManager(cfg, log)
	if err != nil {
		log.Fatalf("Failed to create certificate manager: %v", err)
	}
	factory := container.StandaloneFactory{
		Log: log,
		Cfg: cfg,
		Db:  dbconn,
	}
	c := container.StandaloneContainer{
		Factory: &factory,
		Cert:    certmanager,
	}
	if !cert.IsServerCertificateGenerated() {
		certmanager.GenerateServer(true)
	}

	if !agentservice.IsRegistered() {
		agentservice.Register(context.TODO(), log, cfg, &c)
	}

	return &c
}
