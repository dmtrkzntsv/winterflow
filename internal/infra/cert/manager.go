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
	validityYears = 10
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

// GenerateServer ensures a complete, internally consistent set of server
// certificate material. Artifacts are not independent, so missing pieces
// cascade instead of being filled in blindly:
//
//   - the CA key and certificate are a pair — regenerating one half would
//     leave the survivor useless, so losing either regenerates both AND
//     everything signed by the old CA;
//   - a new server key invalidates the existing server certificate;
//   - a new server certificate (or CA) invalidates the full chain.
func (m *Manager) GenerateServer(override bool) error {
	exists := func(name string, fn func() (bool, error)) (bool, error) {
		ok, err := fn()
		if err != nil {
			return false, fmt.Errorf("check %s: %w", name, err)
		}
		return ok, nil
	}
	generate := func(name, p string, fn func() error) error {
		m.log.Info("generating certificate artifact", "artifact", name, "path", path.Join(m.cfg.GetHubCertDir(), p), "override", override)
		if err := fn(); err != nil {
			return fmt.Errorf("generate %s: %w", name, err)
		}
		return nil
	}

	caKeyOK, err := exists("CA private key", m.ExistsCAKey)
	if err != nil {
		return err
	}
	caCertOK, err := exists("CA certificate", m.ExistsCACertificate)
	if err != nil {
		return err
	}
	regenCA := override || !caKeyOK || !caCertOK
	if regenCA {
		if !override && (caKeyOK || caCertOK) {
			m.log.Warn("CA key/cert pair incomplete — regenerating the CA and everything signed by it")
		}
		if err := generate("CA private key", m.paths.CAKey, m.GenerateCAKey); err != nil {
			return err
		}
		if err := generate("CA certificate", m.paths.CACert, m.GenerateCACertificate); err != nil {
			return err
		}
	}

	serverKeyOK, err := exists("server private key", m.ExistsServerKey)
	if err != nil {
		return err
	}
	regenServerKey := override || !serverKeyOK
	if regenServerKey {
		if err := generate("server private key", m.paths.ServerKey, m.GenerateServerKey); err != nil {
			return err
		}
	}

	serverCertOK, err := exists("server certificate", m.ExistsServerCertificate)
	if err != nil {
		return err
	}
	regenServerCert := regenCA || regenServerKey || !serverCertOK
	if regenServerCert {
		if err := generate("server certificate", m.paths.ServerCert, m.GenerateServerCertificate); err != nil {
			return err
		}
	}

	fullChainOK, err := exists("full-chain certificate", m.ExistsFullchainCertificate)
	if err != nil {
		return err
	}
	if regenServerCert || !fullChainOK {
		if err := generate("full-chain certificate", m.paths.ServerFullChain, m.GenerateFullchainCertificate); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) GenerateAgent(certificateID string) (*time.Time, error) {
	var expiresAt *time.Time

	if err := m.GenerateAgentKey(); err != nil {
		return expiresAt, fmt.Errorf("generate agent key: %w", err)
	}

	if err := m.GenerateAgentCSR(certificateID); err != nil {
		return expiresAt, fmt.Errorf("generate agent csr: %w", err)
	}

	expAt, err := m.GenerateAgentCertificate()
	if err != nil {
		return expiresAt, fmt.Errorf("generate agent certificate: %w", err)
	}
	expiresAt = expAt

	return expiresAt, nil
}

func (m *Manager) DeleteAgent() error {
	if err := m.generator.DeleteAgentKey(); err != nil {
		return fmt.Errorf("delete agent key: %w", err)
	}

	if err := m.DeleteAgentCSR(); err != nil {
		return fmt.Errorf("delete agent csr: %w", err)
	}

	err := m.DeleteAgentCertificate()
	if err != nil {
		return fmt.Errorf("delete agent certificate: %w", err)
	}

	return nil
}

func (m *Manager) GenerateCAKey() error {
	return m.generator.GenerateCAKey()
}

func (m *Manager) ExistsCAKey() (bool, error) {
	return m.generator.ExistsCAKey()
}

func (m *Manager) DeleteCAKey() error {
	return m.generator.DeleteCAKey()
}

func (m *Manager) GenerateCACertificate() error {
	return m.generator.GenerateCACertificate()
}

func (m *Manager) ExistsCACertificate() (bool, error) {
	return m.generator.ExistsCACertificate()
}

func (m *Manager) DeleteCACertificate() error {
	return m.generator.DeleteCACertificate()
}

func (m *Manager) GenerateServerKey() error {
	return m.generator.GenerateServerKey()
}

func (m *Manager) ExistsServerKey() (bool, error) {
	return m.generator.ExistsServerKey()
}

func (m *Manager) DeleteServerKey() error {
	return m.generator.DeleteServerKey()
}

func (m *Manager) DeleteServerCSR() error {
	return m.generator.DeleteServerCSR()
}

func (m *Manager) GenerateServerCertificate() error {
	return m.generator.GenerateServerCertificate()
}

func (m *Manager) ExistsServerCertificate() (bool, error) {
	return m.generator.ExistsServerCertificate()
}

func (m *Manager) DeleteServerCertificate() error {
	return m.generator.DeleteServerCertificate()
}

func (m *Manager) GenerateFullchainCertificate() error {
	return m.generator.GenerateFullchainCertificate()
}

func (m *Manager) ExistsFullchainCertificate() (bool, error) {
	return m.generator.ExistsFullchainCertificate()
}

func (m *Manager) DeleteFullchainCertificate() error {
	return m.generator.DeleteFullchainCertificate()
}

func (m *Manager) GenerateAgentKey() error {
	return m.generator.GenerateAgentKey()
}

func (m *Manager) ExistsAgentKey() (bool, error) {
	return m.generator.ExistsAgentKey()
}

func (m *Manager) DeleteAgentKey() error {
	return m.generator.DeleteAgentKey()
}

func (m *Manager) GenerateAgentCSR(certificateID string) error {
	return m.generator.GenerateAgentCSR(certificateID)
}

func (m *Manager) ExistsAgentCSR() (bool, error) {
	return m.generator.ExistsAgentCSR()
}

func (m *Manager) DeleteAgentCSR() error {
	return m.generator.DeleteAgentCSR()
}

func (m *Manager) GenerateAgentCertificate() (*time.Time, error) {
	return m.generator.GenerateAgentCertificate()
}

func (m *Manager) ExistsAgentCertificate() (bool, error) {
	return m.generator.ExistsAgentCertificate()
}

func (m *Manager) DeleteAgentCertificate() error {
	return m.generator.DeleteAgentCertificate()
}

func (m *Manager) GetAgentCertificate() ([]byte, error) {
	return m.generator.GetAgentCertificate()
}
