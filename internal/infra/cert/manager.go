package cert

import (
	"fmt"

	certpkg "winterflow/pkg/cert"
)

type Manager struct {
	generator *certpkg.Generator
}

func NewManager(outputPath, extPath string) (*Manager, error) {
	gen, err := certpkg.NewGenerator(outputPath, extPath)
	if err != nil {
		return nil, fmt.Errorf("create cert generator: %w", err)
	}

	return &Manager{
		generator: gen,
	}, nil
}

func (m *Manager) GenerateCAKey() error {
	return m.generator.GenerateCAKey()
}

func (m *Manager) GenerateCACertificate() error {
	return m.generator.GenerateCACertificate()
}

func (m *Manager) GenerateServerKey() error {
	return m.generator.GenerateServerKey()
}

func (m *Manager) GenerateServerCertificate() error {
	return m.generator.GenerateServerCertificate()
}
