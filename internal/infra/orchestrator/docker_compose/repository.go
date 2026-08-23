// Package dockercompose deploys and inspects apps via the `docker compose`
// CLI. It shells out rather than using the Docker SDK to stay
// dependency-light, as v1 did.
//
// On-disk layout (rooted at cfg.GetAgentDataDir()):
//
//	apps/                        -- human-readable view: {slug} -> ../apps-data/{appID}
//	apps-data/{appID}/           -- canonical app folder, a git repository:
//	  .winterflow/config.json    --   committed app config blob
//	  .winterflow/secrets.json   --   committed, ECIES ciphertext only
//	  .winterflow/source.lock    --   committed upstream SHA (git-sourced apps)
//	  compose.yml, <files...>    --   committed verbatim
//	  .env                       --   committed plain variables
//	  .env.secrets, source/      --   gitignored deploy artifacts
//
// The folder IS the deployment: compose runs in it directly, every save is a
// commit, and rollbacks restore old trees as new commits.
package dockercompose

import (
	"sync"
	"time"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type Repository struct {
	cfg *config.ServerConfig
	log *logger.Logger

	// sourceChecks remembers when each git-sourced app's upstream was last
	// polled, so RefreshDueSources honors per-app intervals across ticks.
	// In-memory only: a restart simply re-checks everything.
	sourceMu     sync.Mutex
	sourceChecks map[string]time.Time

	// versionCache caches each app's commit count keyed by HEAD, because app
	// histories are append-only and unlimited — recounting from scratch on
	// every listing would get slower for as long as the app lives.
	versionMu    sync.Mutex
	versionCache map[string]versionEntry
}

// versionEntry is a cached commit count, valid while HEAD is unchanged.
type versionEntry struct {
	head  string
	count int
}

func NewRepository(cfg *config.ServerConfig, log *logger.Logger) *Repository {
	return &Repository{
		cfg:          cfg,
		log:          log,
		sourceChecks: make(map[string]time.Time),
		versionCache: make(map[string]versionEntry),
	}
}

// forgetApp drops an app's in-memory bookkeeping after deletion.
func (r *Repository) forgetApp(appID string) {
	r.sourceMu.Lock()
	delete(r.sourceChecks, appID)
	r.sourceMu.Unlock()
	r.versionMu.Lock()
	delete(r.versionCache, appID)
	r.versionMu.Unlock()
}
