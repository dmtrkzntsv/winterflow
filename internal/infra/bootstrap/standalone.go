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
		Log:     log,
		Cfg:     cfg,
	}
	if !cert.IsServerCertificateGenerated(certmanager) {
		certmanager.GenerateServer(true)
	}

	agentservice := agentsrv.NewAgentService(&c)
	if !agentservice.IsRegistered() {
		agentservice.Register(context.TODO())
	}

	return &c
}
