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
//	  .winterflow/storage.env    --   gitignored: referenced WF_STORAGE_ values
//	storages/{name}.json         -- registered storages (0600, secret encrypted)
//	backups/state.json           -- the backup scheduler's state
//	backups/tmp/                 -- restore downloads (removed after each restore)
//
// The folder IS the deployment: compose runs in it directly, every save is a
// commit, and rollbacks restore old trees as new commits.
package dockercompose

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/storages"
	"winterflow/internal/infra/backup/store"
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

	// restores holds the apps a restore is running on (see beginRestore):
	// each entry's channel is closed when that restore ends.
	restoreMu sync.Mutex
	restores  map[string]chan struct{}
	// ops counts the operations in flight per app (see beginOp): a restore
	// does not begin while one runs.
	ops map[string]int
	// updating is set once an agent update is under way (see beginUpdate):
	// no restore begins then, the agent is about to exit.
	updating bool
	// appLocks serialize the writers of an app's committed files (see
	// lockApp) and the operations on its source/ checkout (lockSource),
	// under lockMu. An entry lives only while the lock is held or awaited.
	lockMu   sync.Mutex
	appLocks map[string]*appLock
	// marks are the restores in progress, mirrored in backups/restoring.json
	// (see RestoreMark), under markMu.
	markMu sync.Mutex
	marks  map[string]RestoreMark
	// restoreWait bounds how long DeleteApp waits for a restore of its app
	// to end (replaced in tests).
	restoreWait time.Duration
	// diskFree is the room left on the file system holding a path (replaced
	// in tests).
	diskFree func(path string) (uint64, error)

	// storages is the agent's storage registry ({data}/storages/): what
	// storage.* commands manage and WF_STORAGE_ variables resolve against.
	storages      *storages.Registry
	storageLimits storageLimits

	// The steps of the storage commands that touch a storage or Docker,
	// replaced in tests: opening a storage's driver (store.Open), which
	// compose projects are up, and bringing an app up again.
	openStorage     func(s model.Storage, secret string) (store.Store, error)
	runningProjects func(ctx context.Context) (map[string]bool, error)
	redeploy        func(ctx context.Context, appID string) error
	// runCompose runs one docker compose command in an app's folder
	// (replaced in tests to record the arguments); composeOutput runs one
	// and returns its stdout (docker compose config).
	runCompose    func(ctx context.Context, dir string, args []string) error
	composeOutput func(ctx context.Context, dir string, args []string) ([]byte, error)

	// mountCache holds each app's resolved compose model for backups,
	// keyed by HEAD and the derived env files (see appComposeModel).
	mountMu    sync.Mutex
	mountCache map[string]mountCacheEntry

	// hostStat stats a bind source on the host (replaced in tests).
	hostStat func(path string) (fs.FileInfo, error)
	// statFlight holds the host stats still running, one per path: a dead
	// share leaves one blocked goroutine, however often it is asked about.
	statMu     sync.Mutex
	statFlight map[string]*statCall

	// Backup runs: the helper image is pulled once; runHelper runs one
	// helper container, docker one short docker command, helperBinary
	// finds the binary the helper runs, now is the clock (all replaced in
	// tests).
	helperImageReady atomic.Bool
	runHelper        func(ctx context.Context, name string, args []string, stdout io.Writer) (exit int, stderr []byte, err error)
	runRestoreHelper func(ctx context.Context, name string, args []string, stdin io.Reader) (exit int, stderr []byte, err error)
	docker           func(ctx context.Context, args ...string) error
	helperBinary     func() (string, error)
	now              func() time.Time
}

// versionEntry is a cached commit count, valid while HEAD is unchanged.
type versionEntry struct {
	head  string
	count int
}

func NewRepository(cfg *config.ServerConfig, log *logger.Logger) *Repository {
	r := &Repository{
		cfg:              cfg,
		log:              log,
		sourceChecks:     make(map[string]time.Time),
		versionCache:     make(map[string]versionEntry),
		storages:         storages.NewRegistry(filepath.Join(cfg.GetAgentDataDir(), "storages")),
		storageLimits:    defaultStorageLimits,
		openStorage:      store.Open,
		runCompose:       dockerIn,
		composeOutput:    dockerOutputIn,
		mountCache:       make(map[string]mountCacheEntry),
		hostStat:         os.Stat,
		runHelper:        runDockerHelper,
		runRestoreHelper: runDockerRestoreHelper,
		docker:           dockerCombined,
		helperBinary:     agentExecutable,
		now:              time.Now,
		restoreWait:      restoreWaitForDelete,
		diskFree:         statfsFree,
	}
	r.runningProjects = r.composeProjectsUp
	r.redeploy = r.deploy
	return r
}

// forgetApp drops an app's in-memory bookkeeping after deletion.
func (r *Repository) forgetApp(appID string) {
	r.sourceMu.Lock()
	delete(r.sourceChecks, appID)
	r.sourceMu.Unlock()
	r.versionMu.Lock()
	delete(r.versionCache, appID)
	r.versionMu.Unlock()
	r.mountMu.Lock()
	delete(r.mountCache, appID)
	r.mountMu.Unlock()
	r.restoreMu.Lock()
	delete(r.restores, appID)
	delete(r.ops, appID)
	r.restoreMu.Unlock()
	r.clearRestoreMark(appID)
	// The app's locks are not dropped here: a holder or waiter may still be
	// on them, and a second holder on a new lock would run beside it (an app
	// created again under the id); an idle lock removes itself.
}

// appLock is one keyed lock; refs counts its holder and waiters.
type appLock struct {
	ch   chan struct{}
	refs int
}

// lockKeyed takes the lock named key, waiting until it is free or ctx is
// done. The entry is shared by everyone holding or awaiting it and removed
// when the last lets go, so an idle app costs nothing and a lock is never
// replaced under a holder.
func (r *Repository) lockKeyed(ctx context.Context, key string) (unlock func(), err error) {
	r.lockMu.Lock()
	l := r.appLocks[key]
	if l == nil {
		if r.appLocks == nil {
			r.appLocks = map[string]*appLock{}
		}
		l = &appLock{ch: make(chan struct{}, 1)}
		r.appLocks[key] = l
	}
	l.refs++
	r.lockMu.Unlock()
	drop := func() {
		r.lockMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(r.appLocks, key)
		}
		r.lockMu.Unlock()
	}
	select {
	case l.ch <- struct{}{}:
		return func() { <-l.ch; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, fmt.Errorf("another change to the app is still being written: %w", ctx.Err())
	}
}

// lockApp serializes the writers of an app's committed files (save, rename,
// rollback, backup settings, a source poll's commit): the check of a backups
// base and the commit that follows it, in particular, must not interleave
// with another writer's. The distributed agent runs each request on its own
// goroutine. It waits until the lock is free or ctx is done; release it with
// the returned func, before any redeploy. It is held for file work and one
// commit, never for compose or for a clone or fetch of the upstream
// (lockSource covers those); only a rollback, which writes files and moves
// source/ in one step, holds it across the checkout.
func (r *Repository) lockApp(ctx context.Context, appID string) (unlock func(), err error) {
	return r.lockKeyed(ctx, appID)
}

// lockSource serializes the operations on an app's source/ checkout (clone,
// fetch, checkout, the source.lock they write). They are the slow, networked
// part of a save and must not run beside one another, but must not hold up
// the writers of the app's files either. Where both are needed (a rollback)
// lockApp is taken first.
func (r *Repository) lockSource(ctx context.Context, appID string) (unlock func(), err error) {
	return r.lockKeyed(ctx, "source:"+appID)
}
