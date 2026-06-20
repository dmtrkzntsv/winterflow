// Package dockercompose deploys and inspects apps via the `docker compose` CLI.
//
// It is the v2 port of the v1 agent's docker_compose orchestrator, trimmed to
// what the first vertical slice needs: persist an app revision, render its
// templated files, bring it up, and report container status. It shells out to
// `docker compose` rather than the Docker SDK to stay dependency-light, exactly
// as v1's compose_cmd helpers did.
//
// On-disk layout (rooted at cfg.GetAgentDataDir()):
//
//	apps_templates/{appID}/{revision}/config.json   -- app metadata (JSON)
//	apps_templates/{appID}/{revision}/vars/values.json
//	apps_templates/{appID}/{revision}/files/{id}     -- raw template files
//	apps/{appID}/                                    -- rendered, running deployment
package dockercompose

import (
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// maxRevisions is the number of historical revisions kept per app; older ones
// are pruned after a successful save (matches v1's behaviour).
const maxRevisions = 3

type Repository struct {
	cfg *config.ServerConfig
	log *logger.Logger
}

func NewRepository(cfg *config.ServerConfig, log *logger.Logger) *Repository {
	return &Repository{cfg: cfg, log: log}
}
