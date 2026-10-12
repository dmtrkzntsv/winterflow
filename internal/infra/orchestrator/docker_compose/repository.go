// Package dockercompose deploys and inspects apps via the `docker compose`
// CLI. It shells out rather than using the Docker SDK to stay
// dependency-light, as v1 did.
//
// On-disk layout (rooted at cfg.GetAgentDataDir()):
//
//	apps/                        -- human-readable view: {slug} -> ../apps-data/{appID}
//	vars/snapshot.json           -- last vars.sync (0600, ciphertext only)
//	apps-data/{appID}/           -- canonical app folder, a git repository:
//	  .winterflow/config.json    --   committed app config blob
//	  .winterflow/secrets.json   --   committed, ECIES ciphertext only
//	  .winterflow/source.lock    --   committed upstream SHA (git-sourced apps)
//	  compose.yml, <files...>    --   committed verbatim
//	  .env                       --   committed plain variables
//	  .env.secrets, source/      --   gitignored deploy artifacts
//	  .env.shared(.secrets)      --   gitignored, rendered shared variables
//
// The folder IS the deployment: compose runs in it directly, every save is a
// commit, and rollbacks restore old trees as new commits.
package dockercompose

import (
	"path/filepath"
	"sync"
	"time"

	agentvars "winterflow/internal/infra/vars"
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

	// vars is the last vars.sync snapshot ({data}/vars/snapshot.json); tz is
	// the host timezone for WF_TZ, read once.
	vars *agentvars.Store
	tz   string

	// refCache holds each app's referenced shared names keyed by HEAD +
	// source.lock, so stale checks rescan only apps that changed.
	refMu    sync.Mutex
	refCache map[string]refEntry

	// sharedMu serializes rendering the shared files with recomputing the
	// stale set, so a recompute that read the files before a render cannot
	// store a flag the render just cleared.
	sharedMu sync.Mutex

	// stale is the last computed stale set (app id -> names); staleReady is
	// false until the first computation after a start.
	staleMu    sync.Mutex
	stale      map[string][]string
	staleReady bool
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
		vars:         agentvars.NewStore(filepath.Join(cfg.GetAgentDataDir(), "vars")),
		tz:           hostTimezone(),
		refCache:     make(map[string]refEntry),
		stale:        make(map[string][]string),
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
	r.refMu.Lock()
	delete(r.refCache, appID)
	r.refMu.Unlock()
	r.staleMu.Lock()
	delete(r.stale, appID)
	r.staleMu.Unlock()
}
