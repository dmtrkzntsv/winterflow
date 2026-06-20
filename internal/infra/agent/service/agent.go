package service

import (
	"context"
	"os"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/cert"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

type AgentService struct {
	log        *logger.Logger
	cert       *cert.Manager
	serverRepo port.ServerRepository
}

func NewAgentService(log *logger.Logger, certManager *cert.Manager, serverRepo port.ServerRepository) *AgentService {
	return &AgentService{
		log:        log,
		cert:       certManager,
		serverRepo: serverRepo,
	}
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
	exists, _ := as.cert.ExistsAgentKey()
	if !exists {
		return false
	}
	exists, _ = as.cert.ExistsAgentCertificate()
	if !exists {
		return false
	}
	exists, _ = as.cert.ExistsAgentCSR()
	if !exists {
		return false
	}
	return true
}

func (as *AgentService) Register(ctx context.Context, code string) (string, error) {
	serverID := util.GenerateID()
	certificateID := util.GenerateID()
	expiresAt, err := as.cert.GenerateAgent(certificateID)
	if err != nil {
		as.log.Fatalf("Failed to generate agent certificates: %v", err)
	}
	cert, err := as.cert.GetAgentCertificate()
	if err != nil {
		as.log.Fatalf("Failed to get agent certificate: %v", err)
		return "", err
	}
	err = as.serverRepo.RegisterServer(ctx, dto.ServerRegistrationDTO{
		ServerID:      serverID,
		CertificateID: certificateID,
		Hostname: func() string {
			name, _ := os.Hostname()
			return name
		}(),
		Code:                 code,
		ExpiresAt:            time.Now().AddDate(0, 0, 1),
		Certificate:          cert,
		CertificateExpiresAt: *expiresAt,
	})
	if err != nil {
		as.log.Fatalf("Failed to register server, removing the agent certificates: %v", err)
		err = as.cert.DeleteAgent()
		if err != nil {
			as.log.Fatalf("Failed to clean up agent certificates after registration failure: %v", err)
		}
		return "", err
	}

	return serverID, nil
}
