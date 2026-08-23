package cert

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testValidityYears = 10
	testCASubject     = "/C=CA/O=WinterFlow Test/OU=CA/CN=Test CA/emailAddress=ca@test.local"
	testServerSubject = "/C=CA/O=WinterFlow Test/OU=SERVER/CN=localhost"
)

func testPaths() Paths {
	return Paths{
		CAKey:           "ca.key",
		CACert:          "ca.crt",
		ServerKey:       "hub.key",
		ServerCSR:       "hub.csr",
		ServerCert:      "hub.crt",
		ServerFullChain: "hub_fullchain.crt",
		AgentKey:        "agent.key",
		AgentCSR:        "agent.csr",
		AgentCert:       "agent.crt",
	}
}

func writeExtFile(t *testing.T, dir, content string) string {
	t.Helper()
	extPath := filepath.Join(dir, "ext.cnf")
	if err := os.WriteFile(extPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write ext file: %v", err)
	}
	return extPath
}

const defaultExtContent = `[v3_ext]
# comment line to be skipped
basicConstraints = CA:FALSE

[alt_names]
DNS.1 = localhost
DNS.2 = hub.test.local
IP.1 = 127.0.0.1
IP.2 = ::1
`

// newTestGenerator builds a Generator whose output dir does not exist yet, so
// ensureOutputDir is exercised too.
func newTestGenerator(t *testing.T) (*Generator, string) {
	t.Helper()
	dir := t.TempDir()
	extPath := writeExtFile(t, dir, defaultExtContent)
	outDir := filepath.Join(dir, "certs")
	g, err := NewGenerator(outDir, extPath, testPaths(), testCASubject, testServerSubject, testValidityYears)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	return g, outDir
}

func generateCA(t *testing.T, g *Generator) {
	t.Helper()
	if err := g.GenerateCAKey(); err != nil {
		t.Fatalf("GenerateCAKey: %v", err)
	}
	if err := g.GenerateCACertificate(); err != nil {
		t.Fatalf("GenerateCACertificate: %v", err)
	}
}

func parseCertFile(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("no CERTIFICATE PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate %s: %v", path, err)
	}
	return cert
}

func TestNewGeneratorValidation(t *testing.T) {
	dir := t.TempDir()
	extPath := writeExtFile(t, dir, defaultExtContent)
	missingServerCert := testPaths()
	missingServerCert.ServerCert = ""

	cases := []struct {
		name          string
		outputPath    string
		extPath       string
		paths         Paths
		caSubject     string
		serverSubject string
		validity      int
	}{
		{"empty output path", "", extPath, testPaths(), testCASubject, testServerSubject, 1},
		{"empty ext path", dir, "", testPaths(), testCASubject, testServerSubject, 1},
		{"missing paths field", dir, extPath, missingServerCert, testCASubject, testServerSubject, 1},
		{"empty CA subject", dir, extPath, testPaths(), "", testServerSubject, 1},
		{"CA subject without CN", dir, extPath, testPaths(), "/O=Acme", testServerSubject, 1},
		{"empty server subject", dir, extPath, testPaths(), testCASubject, "   ", 1},
		{"invalid subject component", dir, extPath, testPaths(), "/CN", testServerSubject, 1},
		{"unsupported subject attribute", dir, extPath, testPaths(), "/CN=x/ZZ=y", testServerSubject, 1},
		{"zero validity", dir, extPath, testPaths(), testCASubject, testServerSubject, 0},
		{"negative validity", dir, extPath, testPaths(), testCASubject, testServerSubject, -3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewGenerator(tc.outputPath, tc.extPath, tc.paths, tc.caSubject, tc.serverSubject, tc.validity); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestSubjectFromString(t *testing.T) {
	name, err := subjectFromString("/C=CA/ST=QC/L=Montreal/O=WinterFlow/OU=CA/CN=Test CA/emailAddress=info@test.local")
	if err != nil {
		t.Fatalf("subjectFromString: %v", err)
	}
	if got := name.CommonName; got != "Test CA" {
		t.Errorf("CommonName = %q, want %q", got, "Test CA")
	}
	if len(name.Country) != 1 || name.Country[0] != "CA" {
		t.Errorf("Country = %v, want [CA]", name.Country)
	}
	if len(name.Province) != 1 || name.Province[0] != "QC" {
		t.Errorf("Province = %v, want [QC]", name.Province)
	}
	if len(name.Locality) != 1 || name.Locality[0] != "Montreal" {
		t.Errorf("Locality = %v, want [Montreal]", name.Locality)
	}
	if len(name.Organization) != 1 || name.Organization[0] != "WinterFlow" {
		t.Errorf("Organization = %v, want [WinterFlow]", name.Organization)
	}
	if len(name.OrganizationalUnit) != 1 || name.OrganizationalUnit[0] != "CA" {
		t.Errorf("OrganizationalUnit = %v, want [CA]", name.OrganizationalUnit)
	}
	if len(name.ExtraNames) != 1 || !name.ExtraNames[0].Type.Equal(emailAddressOID) || name.ExtraNames[0].Value != "info@test.local" {
		t.Errorf("ExtraNames = %v, want emailAddress=info@test.local", name.ExtraNames)
	}

	// Empty non-CN values are skipped, no leading slash is fine.
	name, err = subjectFromString("O=/CN=only-cn")
	if err != nil {
		t.Fatalf("subjectFromString without leading slash: %v", err)
	}
	if len(name.Organization) != 0 {
		t.Errorf("empty O should be skipped, got %v", name.Organization)
	}
	if name.CommonName != "only-cn" {
		t.Errorf("CommonName = %q, want only-cn", name.CommonName)
	}

	if _, err := subjectFromString(""); err == nil {
		t.Error("expected error for empty subject")
	}
	if _, err := subjectFromString("/CN=x/=y"); err == nil {
		t.Error("expected error for empty key")
	}
}

func TestGenerateCA(t *testing.T) {
	g, outDir := newTestGenerator(t)

	if exists, err := g.ExistsCAKey(); err != nil || exists {
		t.Fatalf("ExistsCAKey before generation = (%v, %v), want (false, nil)", exists, err)
	}
	if exists, err := g.ExistsCACertificate(); err != nil || exists {
		t.Fatalf("ExistsCACertificate before generation = (%v, %v), want (false, nil)", exists, err)
	}

	generateCA(t, g)

	if exists, err := g.ExistsCAKey(); err != nil || !exists {
		t.Fatalf("ExistsCAKey after generation = (%v, %v), want (true, nil)", exists, err)
	}
	if exists, err := g.ExistsCACertificate(); err != nil || !exists {
		t.Fatalf("ExistsCACertificate after generation = (%v, %v), want (true, nil)", exists, err)
	}

	// The key must be a valid P-256 EC key.
	if _, err := loadECPrivateKey(filepath.Join(outDir, "ca.key")); err != nil {
		t.Fatalf("generated CA key invalid: %v", err)
	}

	caCert := parseCertFile(t, filepath.Join(outDir, "ca.crt"))
	if !caCert.IsCA {
		t.Error("CA certificate should have IsCA=true")
	}
	if caCert.Subject.CommonName != "Test CA" {
		t.Errorf("CA CommonName = %q, want %q", caCert.Subject.CommonName, "Test CA")
	}
	if caCert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA certificate should allow cert signing")
	}

	now := time.Now()
	if caCert.NotBefore.After(now) {
		t.Errorf("NotBefore %v is in the future (padding should backdate it)", caCert.NotBefore)
	}
	wantNotAfter := now.AddDate(testValidityYears, 0, 0)
	if diff := caCert.NotAfter.Sub(wantNotAfter); diff < -time.Minute || diff > time.Minute {
		t.Errorf("NotAfter = %v, want ~%v", caCert.NotAfter, wantNotAfter)
	}
	if len(caCert.SubjectKeyId) == 0 {
		t.Error("CA certificate should have a SubjectKeyId")
	}
}

func TestGenerateCACertificateWithoutKey(t *testing.T) {
	g, _ := newTestGenerator(t)
	if err := g.GenerateCACertificate(); err == nil {
		t.Fatal("expected error when CA key does not exist")
	}
}

func TestGenerateServerCertificate(t *testing.T) {
	g, outDir := newTestGenerator(t)
	generateCA(t, g)

	if err := g.GenerateServerKey(); err != nil {
		t.Fatalf("GenerateServerKey: %v", err)
	}
	if exists, err := g.ExistsServerKey(); err != nil || !exists {
		t.Fatalf("ExistsServerKey = (%v, %v), want (true, nil)", exists, err)
	}

	if err := g.GenerateServerCertificate(); err != nil {
		t.Fatalf("GenerateServerCertificate: %v", err)
	}
	for _, check := range []struct {
		name   string
		exists func() (bool, error)
	}{
		{"server cert", g.ExistsServerCertificate},
		{"fullchain", g.ExistsFullchainCertificate},
	} {
		if exists, err := check.exists(); err != nil || !exists {
			t.Fatalf("%s exists = (%v, %v), want (true, nil)", check.name, exists, err)
		}
	}

	caCert := parseCertFile(t, filepath.Join(outDir, "ca.crt"))
	serverCert := parseCertFile(t, filepath.Join(outDir, "hub.crt"))

	if err := serverCert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("server certificate not signed by CA: %v", err)
	}
	if serverCert.Subject.CommonName != "localhost" {
		t.Errorf("server CommonName = %q, want localhost", serverCert.Subject.CommonName)
	}

	// SANs come from the ext file.
	wantDNS := map[string]bool{"localhost": false, "hub.test.local": false}
	for _, d := range serverCert.DNSNames {
		wantDNS[d] = true
	}
	for d, found := range wantDNS {
		if !found {
			t.Errorf("server certificate missing DNS SAN %q (got %v)", d, serverCert.DNSNames)
		}
	}
	foundIP := false
	for _, ip := range serverCert.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			foundIP = true
		}
	}
	if !foundIP {
		t.Errorf("server certificate missing IP SAN 127.0.0.1 (got %v)", serverCert.IPAddresses)
	}

	hasServerAuth := false
	for _, eku := range serverCert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
		}
	}
	if !hasServerAuth {
		t.Error("server certificate should have serverAuth EKU")
	}

	// The full chain must contain the server certificate followed by the CA.
	chainData, err := os.ReadFile(filepath.Join(outDir, "hub_fullchain.crt"))
	if err != nil {
		t.Fatalf("read fullchain: %v", err)
	}
	var chain []*x509.Certificate
	rest := chainData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parse fullchain block: %v", err)
		}
		chain = append(chain, c)
	}
	if len(chain) != 2 {
		t.Fatalf("fullchain contains %d certificates, want 2", len(chain))
	}
	if !chain[0].Equal(serverCert) || !chain[1].Equal(caCert) {
		t.Error("fullchain order should be server cert then CA cert")
	}
}

func TestGenerateServerCertificateErrors(t *testing.T) {
	t.Run("missing CA", func(t *testing.T) {
		g, _ := newTestGenerator(t)
		if err := g.GenerateServerKey(); err != nil {
			t.Fatalf("GenerateServerKey: %v", err)
		}
		if err := g.GenerateServerCertificate(); err == nil {
			t.Fatal("expected error when CA is missing")
		}
	})

	t.Run("missing ext file", func(t *testing.T) {
		dir := t.TempDir()
		g, err := NewGenerator(dir, filepath.Join(dir, "does-not-exist.cnf"), testPaths(), testCASubject, testServerSubject, 1)
		if err != nil {
			t.Fatalf("NewGenerator: %v", err)
		}
		generateCA(t, g)
		if err := g.GenerateServerKey(); err != nil {
			t.Fatalf("GenerateServerKey: %v", err)
		}
		if err := g.GenerateServerCertificate(); err == nil {
			t.Fatal("expected error when ext file is missing")
		}
	})

	t.Run("invalid IP in ext file", func(t *testing.T) {
		dir := t.TempDir()
		extPath := writeExtFile(t, dir, "DNS.1 = localhost\nIP.1 = not-an-ip\n")
		g, err := NewGenerator(dir, extPath, testPaths(), testCASubject, testServerSubject, 1)
		if err != nil {
			t.Fatalf("NewGenerator: %v", err)
		}
		generateCA(t, g)
		if err := g.GenerateServerKey(); err != nil {
			t.Fatalf("GenerateServerKey: %v", err)
		}
		err = g.GenerateServerCertificate()
		if err == nil {
			t.Fatal("expected error for invalid IP in ext file")
		}
		if !strings.Contains(err.Error(), "not-an-ip") {
			t.Errorf("error should name the invalid IP, got: %v", err)
		}
	})
}

func TestExtFileNoiseIsSkipped(t *testing.T) {
	dir := t.TempDir()
	extPath := writeExtFile(t, dir, "# only comments\n[section]\nnot-a-kv-line\nbasicConstraints = CA:FALSE\n\n")
	g, err := NewGenerator(dir, extPath, testPaths(), testCASubject, testServerSubject, 1)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	generateCA(t, g)
	if err := g.GenerateServerKey(); err != nil {
		t.Fatalf("GenerateServerKey: %v", err)
	}
	if err := g.GenerateServerCertificate(); err != nil {
		t.Fatalf("GenerateServerCertificate with SAN-less ext file: %v", err)
	}
	serverCert := parseCertFile(t, filepath.Join(dir, "hub.crt"))
	if len(serverCert.DNSNames) != 0 || len(serverCert.IPAddresses) != 0 {
		t.Errorf("expected no SANs, got DNS=%v IP=%v", serverCert.DNSNames, serverCert.IPAddresses)
	}
}

func TestGenerateFullchainCertificate(t *testing.T) {
	g, _ := newTestGenerator(t)

	// Fails while the server certificate does not exist yet.
	if err := g.GenerateFullchainCertificate(); err == nil {
		t.Fatal("expected error when server certificate is missing")
	}

	generateCA(t, g)
	if err := g.GenerateServerKey(); err != nil {
		t.Fatalf("GenerateServerKey: %v", err)
	}
	if err := g.GenerateServerCertificate(); err != nil {
		t.Fatalf("GenerateServerCertificate: %v", err)
	}

	if err := g.deleteArtifact(g.paths.ServerFullChain, "full-chain certificate"); err != nil {
		t.Fatalf("delete fullchain: %v", err)
	}
	if exists, _ := g.ExistsFullchainCertificate(); exists {
		t.Fatal("fullchain should be gone after delete")
	}
	if err := g.GenerateFullchainCertificate(); err != nil {
		t.Fatalf("GenerateFullchainCertificate: %v", err)
	}
	if exists, _ := g.ExistsFullchainCertificate(); !exists {
		t.Fatal("fullchain should exist after regeneration")
	}
}

func TestGenerateAgentFlow(t *testing.T) {
	g, outDir := newTestGenerator(t)
	generateCA(t, g)

	for _, name := range []string{"agent.key", "agent.csr", "agent.crt"} {
		if exists, err := g.pathExists(name); err != nil || exists {
			t.Fatalf("agent artifact %s exists = (%v, %v) before generation, want (false, nil)", name, exists, err)
		}
	}

	if err := g.GenerateAgentKey(); err != nil {
		t.Fatalf("GenerateAgentKey: %v", err)
	}
	if err := g.GenerateAgentCSR("agent-123"); err != nil {
		t.Fatalf("GenerateAgentCSR: %v", err)
	}
	expiresAt, err := g.GenerateAgentCertificate()
	if err != nil {
		t.Fatalf("GenerateAgentCertificate: %v", err)
	}
	if expiresAt == nil {
		t.Fatal("expected non-nil expiry timestamp")
	}
	wantExpiry := time.Now().AddDate(testValidityYears, 0, 0)
	if diff := expiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiresAt = %v, want ~%v", expiresAt, wantExpiry)
	}

	for _, name := range []string{"agent.key", "agent.csr", "agent.crt"} {
		if exists, err := g.pathExists(name); err != nil || !exists {
			t.Fatalf("agent artifact %s exists = (%v, %v) after generation, want (true, nil)", name, exists, err)
		}
	}

	caCert := parseCertFile(t, filepath.Join(outDir, "ca.crt"))
	agentCert := parseCertFile(t, filepath.Join(outDir, "agent.crt"))
	if agentCert.Subject.CommonName != "agent-123" {
		t.Errorf("agent CommonName = %q, want agent-123", agentCert.Subject.CommonName)
	}
	if err := agentCert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("agent certificate not signed by CA: %v", err)
	}
	// The cert's own NotAfter must match the returned expiry (1s DER granularity).
	if diff := agentCert.NotAfter.Sub(*expiresAt); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("cert NotAfter %v != returned expiry %v", agentCert.NotAfter, expiresAt)
	}
	hasClientAuth := false
	for _, eku := range agentCert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageClientAuth {
			hasClientAuth = true
		}
	}
	if !hasClientAuth {
		t.Error("agent certificate should have clientAuth EKU")
	}

	// Reading the cert back returns the exact stored bytes.
	got, err := g.GetAgentCertificate()
	if err != nil {
		t.Fatalf("GetAgentCertificate: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(outDir, "agent.crt"))
	if err != nil {
		t.Fatalf("read agent.crt: %v", err)
	}
	if string(got) != string(want) {
		t.Error("GetAgentCertificate bytes differ from file content")
	}
}

func TestGenerateAgentCSRErrors(t *testing.T) {
	g, _ := newTestGenerator(t)

	if err := g.GenerateAgentCSR("agent-1"); err == nil {
		t.Fatal("expected error when agent key is missing")
	}

	if err := g.GenerateAgentKey(); err != nil {
		t.Fatalf("GenerateAgentKey: %v", err)
	}
	if err := g.GenerateAgentCSR(""); err == nil {
		t.Fatal("expected error for empty certificate ID")
	}
	if err := g.GenerateAgentCSR("   "); err == nil {
		t.Fatal("expected error for whitespace certificate ID")
	}
}

func TestGenerateAgentCertificateErrors(t *testing.T) {
	t.Run("missing CA", func(t *testing.T) {
		g, _ := newTestGenerator(t)
		if err := g.GenerateAgentKey(); err != nil {
			t.Fatalf("GenerateAgentKey: %v", err)
		}
		if err := g.GenerateAgentCSR("agent-1"); err != nil {
			t.Fatalf("GenerateAgentCSR: %v", err)
		}
		if _, err := g.GenerateAgentCertificate(); err == nil {
			t.Fatal("expected error when CA is missing")
		}
	})

	t.Run("missing CSR", func(t *testing.T) {
		g, _ := newTestGenerator(t)
		generateCA(t, g)
		if _, err := g.GenerateAgentCertificate(); err == nil {
			t.Fatal("expected error when agent CSR is missing")
		}
	})
}

func TestGetAgentCertificateMissing(t *testing.T) {
	g, _ := newTestGenerator(t)
	if _, err := g.GetAgentCertificate(); err == nil {
		t.Fatal("expected error when agent certificate does not exist")
	}
}

func TestDeleteArtifacts(t *testing.T) {
	g, _ := newTestGenerator(t)
	generateCA(t, g)
	if err := g.GenerateAgentKey(); err != nil {
		t.Fatalf("GenerateAgentKey: %v", err)
	}
	if err := g.GenerateAgentCSR("agent-1"); err != nil {
		t.Fatalf("GenerateAgentCSR: %v", err)
	}
	if _, err := g.GenerateAgentCertificate(); err != nil {
		t.Fatalf("GenerateAgentCertificate: %v", err)
	}

	deletes := []struct {
		name     string
		del      func() error
		filename string
	}{
		{"agent key", g.DeleteAgentKey, "agent.key"},
		{"agent CSR", g.DeleteAgentCSR, "agent.csr"},
		{"agent cert", g.DeleteAgentCertificate, "agent.crt"},
	}
	for _, d := range deletes {
		if err := d.del(); err != nil {
			t.Errorf("delete %s: %v", d.name, err)
		}
		if exists, err := g.pathExists(d.filename); err != nil || exists {
			t.Errorf("%s exists = (%v, %v) after delete, want (false, nil)", d.name, exists, err)
		}
		// Deleting a missing artifact is a no-op, not an error.
		if err := d.del(); err != nil {
			t.Errorf("second delete of %s should be nil, got %v", d.name, err)
		}
	}
}
