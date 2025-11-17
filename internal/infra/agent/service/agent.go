package service

import (
	"context"
	"os"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/bootstrap/container"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

type AgentService struct {
}

func NewAgentService() *AgentService {
	return &AgentService{}
}

func (as *AgentService) HasConfig() bool {
	return false
}

func (as *AgentService) GenerateConfig(ctx context.Context) error {
	return nil
}

func (as *AgentService) HasKeys() bool {
	return false
}

func (as *AgentService) GenerateKeys(ctx context.Context) error {
	return nil
}

func (as *AgentService) IsRegistered() bool {
	return true
}

func (as *AgentService) Register(ctx context.Context, log *logger.Logger, cfg *config.ServerConfig, c *container.StandaloneContainer) error {
	err := c.Cert.GenerateServer(false)
	if err != nil {
		log.Fatalf("Failed to generate server certificates: %v", err)
	}
	serverID := util.GenerateID()
	certificateID := util.GenerateID()
	expiresAt, err := c.Cert.GenerateAgent(certificateID)
	if err != nil {
		log.Fatalf("Failed to generate agent certificates: %v", err)
	}
	sr := c.Factory.NewServerRepository()
	sr.RegisterServer(context.TODO(), dto.ServerRegistrationDTO{
		ServerID:      serverID,
		CertificateID: certificateID,
		Hostname: func() string {
			name, _ := os.Hostname()
			return name
		}(),
		Code:                 "12345",
		ExpiresAt:            time.Now().AddDate(0, 0, 1),
		Certificate:          []byte{},
		CertificateExpiresAt: *expiresAt,
	})

	return nil
}
