package service

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"winterflow/internal/domain/dto"
	"winterflow/pkg/logger"
)

type fakeRegistrar struct {
	got []dto.ServerRegistrationDTO
	err error
}

func (f *fakeRegistrar) RegisterServer(_ context.Context, d dto.ServerRegistrationDTO) error {
	f.got = append(f.got, d)
	return f.err
}

func testLogger() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

func TestRegisterCreatesOnlyAnAgentIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hub-certs")
	repo := &fakeRegistrar{}
	s := NewAgentService(testLogger(), dir, repo)

	id, err := s.Register(context.Background(), "ABC123")
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.got) != 1 {
		t.Fatalf("registrations = %d", len(repo.got))
	}
	reg := repo.got[0]
	if reg.ServerID != id || reg.Code != "ABC123" || reg.CertificateID == "" {
		t.Errorf("registration = %+v", reg)
	}
	// The stored certificate is the raw PEM of agent.crt, as before.
	onDisk, _ := os.ReadFile(filepath.Join(dir, "agent.crt"))
	if string(reg.Certificate) != string(onDisk) {
		t.Error("registered certificate is not agent.crt's PEM")
	}
	block, _ := pem.Decode(reg.Certificate)
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.CommonName != id || !c.NotAfter.Equal(reg.CertificateExpiresAt) {
		t.Errorf("CN %q, NotAfter %v vs %v", c.Subject.CommonName, c.NotAfter, reg.CertificateExpiresAt)
	}

	// No CA, hub certificate, full chain or CSR any more.
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "agent.crt" || names[1] != "agent.key" {
		t.Errorf("files = %v", names)
	}
}

// An unclaimed reboot registers again with the same id and key.
func TestRegisterKeepsExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	repo := &fakeRegistrar{}
	s := NewAgentService(testLogger(), dir, repo)
	first, err := s.Register(context.Background(), "AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	key1, _ := os.ReadFile(filepath.Join(dir, "agent.key"))
	second, err := s.Register(context.Background(), "BBBBBB")
	if err != nil {
		t.Fatal(err)
	}
	key2, _ := os.ReadFile(filepath.Join(dir, "agent.key"))
	if first != second || string(key1) != string(key2) {
		t.Errorf("ids %q / %q, key kept %v", first, second, string(key1) == string(key2))
	}
	if string(repo.got[0].Certificate) != string(repo.got[1].Certificate) {
		t.Error("certificate changed")
	}
}

func TestRegisterErrors(t *testing.T) {
	if _, err := NewAgentService(testLogger(), "", &fakeRegistrar{}).Register(context.Background(), "X"); err == nil {
		t.Error("empty cert dir accepted")
	}
	repo := &fakeRegistrar{err: errors.New("db down")}
	if _, err := NewAgentService(testLogger(), t.TempDir(), repo).Register(context.Background(), "X"); err == nil {
		t.Error("registration error swallowed")
	}
}
