package service

import (
	"context"
	"os"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/bootstrap/container"
	"winterflow/pkg/util"
)

type AgentService struct {
	c *container.StandaloneContainer
}

func NewAgentService(c *container.StandaloneContainer) *AgentService {
	return &AgentService{
		c: c,
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
	exists, _ := as.c.Cert.ExistsAgentKey()
	if !exists {
		return false
	}
	exists, _ = as.c.Cert.ExistsAgentCertificate()
	if !exists {
		return false
	}
	exists, _ = as.c.Cert.ExistsAgentCSR()
	if !exists {
		return false
	}
	return true
}

func (as *AgentService) Register(ctx context.Context, code string) (string, error) {
	serverID := util.GenerateID()
	certificateID := util.GenerateID()
	expiresAt, err := as.c.Cert.GenerateAgent(certificateID)
	if err != nil {
		as.c.Log.Fatalf("Failed to generate agent certificates: %v", err)
	}
	cert, err := as.c.Cert.GetAgentCertificate()
	if err != nil {
		as.c.Log.Fatalf("Failed to get agent certificate: %v", err)
		return "", err
	}
	sr := as.c.Factory.NewServerRepository()
	err = sr.RegisterServer(context.TODO(), dto.ServerRegistrationDTO{
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
		as.c.Log.Fatalf("Failed to register server, removing the agent certificates: %v", err)
		err = as.c.Cert.DeleteAgent()
		if err != nil {
			as.c.Log.Fatalf("Failed to clean up agent certificates after registration failure: %v", err)
		}
		return "", err
	}

	return serverID, nil
}
