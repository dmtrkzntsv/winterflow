package util

import (
	"context"
	"errors"

	"winterflow/pkg/config"
	"winterflow/pkg/crypto"
)

// CapabilityReader reads a server's agent-published capability.
type CapabilityReader interface {
	GetCapability(ctx context.Context, serverID, name string) (value string, ok bool, err error)
}

// ServerPublicKey is a server's agent EC public key (base64 of the
// uncompressed P-256 point) the browser ECIES-encrypts secrets to. Source
// order: the persisted public_key capability (published by the agent, both
// topologies), then the local agent certificate (standalone, where the API
// shares disk with the agent). Either source may be nil.
func ServerPublicKey(ctx context.Context, caps CapabilityReader, cfg *config.ServerConfig, serverID string) (string, error) {
	if caps != nil {
		if value, ok, err := caps.GetCapability(ctx, serverID, "public_key"); err == nil && ok && value != "" {
			return value, nil
		}
	}
	if cfg != nil {
		return crypto.PublicKeyPointFromCertPath(cfg.GetAgentCertPath())
	}
	return "", errors.New("public key unavailable for this server")
}
