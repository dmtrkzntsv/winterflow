package cert

import (
	"fmt"
	"path"
	"time"

	certpkg "winterflow/pkg/cert"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

const (
	validityYears = 100
)

type Manager struct {
	generator *certpkg.Generator
	cfg       *config.ServerConfig
	log       *logger.Logger
	paths     certpkg.Paths
}

func NewManager(cfg *config.ServerConfig, log *logger.Logger) (*Manager, error) {
	paths := certpkg.Paths{
		CAKey:           cfg.GetHubCAKeyFilename(),
		CACert:          cfg.GetHubCACertFilename(),
		ServerKey:       cfg.GetHubKeyFilename(),
		ServerCSR:       cfg.GetHubCSRFilename(),
		ServerCert:      cfg.GetHubCertFilename(),
		ServerFullChain: cfg.GetHubFullchainFilename(),
		AgentKey:        cfg.GetAgentKeyFilename(),
		AgentCSR:        cfg.GetAgentCSRFilename(),
		AgentCert:       cfg.GetAgentCertFilename(),
	}
	gen, err := certpkg.NewGenerator(cfg.GetHubCertDir(), cfg.GetHubCertExtPath(), paths, cfg.GetHubCASubject(), cfg.GetHubServerSubject(), validityYears)
	if err != nil {
		return nil, fmt.Errorf("create cert generator: %w", err)
	}

	return &Manager{
		generator: gen,
		cfg:       cfg,
		log:       log,
		paths:     paths,
	}, nil
}

func (m *Manager) GenerateServer(override bool) error {
	caKeyPath := path.Join(m.cfg.GetHubCertDir(), m.paths.CAKey)
	caCertPath := path.Join(m.cfg.GetHubCertDir(), m.paths.CACert)
	serverKeyPath := path.Join(m.cfg.GetHubCertDir(), m.paths.ServerKey)
	serverCertPath := path.Join(m.cfg.GetHubCertDir(), m.paths.ServerCert)
	fullChainPath := path.Join(m.cfg.GetHubCertDir(), m.paths.ServerFullChain)

	artifacts := []struct {
		name     string
		path     string
		exists   func() (bool, error)
		generate func() error
	}{
		{"CA private key", caKeyPath, m.ExistsCAKey, m.GenerateCAKey},
		{"CA certificate", caCertPath, m.ExistsCACertificate, m.GenerateCACertificate},
		{"server private key", serverKeyPath, m.ExistsServerKey, m.GenerateServerKey},
		{"server certificate", serverCertPath, m.ExistsServerCertificate, m.GenerateServerCertificate},
		{"full-chain certificate", fullChainPath, m.ExistsFullchainCertificate, m.GenerateFullchainCertificate},
	}

	for _, artifact := range artifacts {
		if !override {
			exists, err := artifact.exists()
			if err != nil {
				return fmt.Errorf("check %s: %w", artifact.name, err)
			}
			if exists {
				m.log.Debug("certificate artifact exists, skipping", "artifact", artifact.name, "path", artifact.path)
				continue
			}
		}

		m.log.Info("generating certificate artifact", "artifact", artifact.name, "path", artifact.path, "override", override)
		if err := artifact.generate(); err != nil {
			return fmt.Errorf("generate %s: %w", artifact.name, err)
		}
	}

	return nil
}

func (m *Manager) GenerateAgent(certificateID string) (*time.Time, error) {
	var expiresAt *time.Time

	if exists, err := m.ExistsAgentKey(); err != nil {
		return expiresAt, fmt.Errorf("check agent key: %w", err)
	} else if !exists {
		if err = m.GenerateAgentKey(); err != nil {
			return expiresAt, fmt.Errorf("generate agent key: %w", err)
		}
	}

	if exists, err := m.ExistsAgentCSR(); err != nil {
		return expiresAt, fmt.Errorf("check agent csr: %w", err)
	} else if !exists {
		if err = m.GenerateAgentCSR(certificateID); err != nil {
			return expiresAt, fmt.Errorf("generate agent csr: %w", err)
		}
	}

	if exists, err := m.ExistsAgentCertificate(); err != nil {
		return expiresAt, fmt.Errorf("check agent certificate: %w", err)
	} else if !exists {
		expAt, err := m.GenerateAgentCertificate()
		if err != nil {
			return expiresAt, fmt.Errorf("generate agent certificate: %w", err)
		}
		expiresAt = expAt
	}
	return expiresAt, nil
}

func (m *Manager) GenerateCAKey() error {
	return m.generator.GenerateCAKey()
}

func (m *Manager) ExistsCAKey() (bool, error) {
	return m.generator.ExistsCAKey()
}

func (m *Manager) GenerateCACertificate() error {
	return m.generator.GenerateCACertificate()
}

func (m *Manager) ExistsCACertificate() (bool, error) {
	return m.generator.ExistsCACertificate()
}

func (m *Manager) GenerateServerKey() error {
	return m.generator.GenerateServerKey()
}

func (m *Manager) ExistsServerKey() (bool, error) {
	return m.generator.ExistsServerKey()
}

func (m *Manager) GenerateServerCertificate() error {
	return m.generator.GenerateServerCertificate()
}

func (m *Manager) ExistsServerCertificate() (bool, error) {
	return m.generator.ExistsServerCertificate()
}

func (m *Manager) GenerateFullchainCertificate() error {
	return m.generator.GenerateFullchainCertificate()
}

func (m *Manager) ExistsFullchainCertificate() (bool, error) {
	return m.generator.ExistsFullchainCertificate()
}

func (m *Manager) GenerateAgentKey() error {
	return m.generator.GenerateAgentKey()
}

func (m *Manager) ExistsAgentKey() (bool, error) {
	return m.generator.ExistsAgentKey()
}

func (m *Manager) GenerateAgentCSR(certificateID string) error {
	return m.generator.GenerateAgentCSR(certificateID)
}

func (m *Manager) ExistsAgentCSR() (bool, error) {
	return m.generator.ExistsAgentCSR()
}

func (m *Manager) GenerateAgentCertificate() (*time.Time, error) {
	return m.generator.GenerateAgentCertificate()
}

func (m *Manager) ExistsAgentCertificate() (bool, error) {
	return m.generator.ExistsAgentCertificate()
}
