package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"
	"winterflow/internal/domain/dto"
	"winterflow/pkg/cert"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Registrar stores the embedded server's pending registration.
type Registrar interface {
	RegisterServer(ctx context.Context, d dto.ServerRegistrationDTO) error
}

// AgentService registers standalone's embedded agent: it keeps the agent
// identity (agent.key + agent.crt in certDir) and leaves one claimable
// registration carrying its certificate.
type AgentService struct {
	log        *logger.Logger
	certDir    string
	serverRepo Registrar
}

func NewAgentService(log *logger.Logger, certDir string, serverRepo Registrar) *AgentService {
	return &AgentService{
		log:        log,
		certDir:    certDir,
		serverRepo: serverRepo,
	}
}

// Register leaves a pending registration for the embedded server under code
// and returns its server id. An agent identity already in certDir keeps its
// id (its CN) and key — anything encrypted to that key stays readable;
// otherwise a new id gets a new identity.
func (as *AgentService) Register(ctx context.Context, code string) (string, error) {
	serverID, ok := cert.AgentIdentityID(as.certDir)
	if !ok {
		serverID = util.GenerateID()
	}
	// The certificate's CN carries the SERVER id: a provisioned agent derives
	// its routing identity from its own cert (see appagent.ResolveAgentID).
	certPEM, created, err := cert.EnsureAgentIdentity(as.certDir, serverID)
	if err != nil {
		return "", fmt.Errorf("ensure agent identity: %w", err)
	}
	if created {
		as.log.Info("agent identity created", "dir", as.certDir, "server_id", serverID)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("decode agent certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse agent certificate: %w", err)
	}
	hostname, _ := os.Hostname()
	err = as.serverRepo.RegisterServer(ctx, dto.ServerRegistrationDTO{
		ServerID:      serverID,
		CertificateID: util.GenerateID(),
		Hostname:      hostname,
		Code:          code,
		// Standalone's embedded agent is not paired by a human typing the code
		// within a short window — it is claimed automatically on first login.
		// Give the registration a generous validity so the box stays claimable
		// across restarts and idle periods.
		ExpiresAt:            time.Now().AddDate(1, 0, 0),
		Certificate:          certPEM,
		CertificateExpiresAt: c.NotAfter,
	})
	if err != nil {
		return "", fmt.Errorf("register server: %w", err)
	}
	return serverID, nil
}
