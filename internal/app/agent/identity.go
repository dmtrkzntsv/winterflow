package agent

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// agentIDFile persists a generated identity under the agent data dir so it
// survives restarts when no certificate names one yet.
const agentIDFile = "agent-id"

// ResolveAgentID determines the agent's stable identity — the id the hub
// routes commands by, which must match the claimed server id. Precedence:
//
//  1. AGENT_ID environment override (operators, tests);
//  2. the mTLS agent certificate's CommonName — provisioning writes the
//     server id there, so a paired agent identifies as its claimed server;
//  3. a generated id persisted to {AGENT_DATA_DIR}/agent-id (pre-pairing
//     fallback; replaced by the certificate identity once provisioned).
//
// Never returns "" and never changes across restarts of the same install.
func ResolveAgentID(cfg *config.ServerConfig, log *logger.Logger) string {
	if id := strings.TrimSpace(os.Getenv("AGENT_ID")); id != "" {
		return id
	}

	if cn := certCommonName(cfg.GetAgentCertPath()); cn != "" {
		return cn
	}

	idPath := filepath.Join(cfg.GetAgentDataDir(), agentIDFile)
	if raw, err := os.ReadFile(idPath); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id
		}
	}

	id := util.GenerateID()
	if err := os.MkdirAll(cfg.GetAgentDataDir(), 0o755); err == nil {
		if err := os.WriteFile(idPath, []byte(id), 0o600); err != nil {
			log.Warn("failed to persist agent id; identity will rotate on restart", "error", err)
		}
	} else {
		log.Warn("failed to create agent data dir for identity", "error", err)
	}
	return id
}

// certCommonName reads the CommonName from a PEM certificate, "" on any
// failure (missing file, unparseable, empty CN).
func certCommonName(certPath string) string {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cert.Subject.CommonName)
}
