package dockercompose

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/crypto"
)

// storageLimits are the time limits of the storage commands (a field of the
// Repository so tests can shrink them).
type storageLimits struct {
	// probe bounds a storage test: opening the storage and the probe write
	// and delete (storage.test, and the test storage.save runs first).
	probe time.Duration
	// redeploy bounds one app's redeploy after storage.save.
	redeploy time.Duration
	// docker bounds the one `docker ps` storage.save runs.
	docker time.Duration
	// saveBudget is the overall budget of one storage.save (probe, docker ps
	// and every redeploy share it). It stays below the dispatch manager's 10
	// minute pending TTL, so the result is not dropped, and bounds how long
	// the command can hold the (standalone: single) pipeline.
	saveBudget time.Duration
	// redeployMinBudget is the least of the save's budget that must remain
	// to start another redeploy.
	redeployMinBudget time.Duration
	// folder bounds the deploy-time check that the referenced local
	// storages' folders exist (all of them, checked concurrently).
	folder time.Duration
}

var defaultStorageLimits = storageLimits{
	probe:             30 * time.Second,
	redeploy:          10 * time.Minute,
	docker:            30 * time.Second,
	saveBudget:        8 * time.Minute,
	redeployMinBudget: 2 * time.Minute,
	folder:            10 * time.Second,
}

// detached runs fn in a goroutine and returns the channel its result
// arrives on. Storage syscalls ignore contexts (a hung hard NFS/CIFS mount
// blocks them indefinitely), so callers wait on the channel and their
// deadline together and abandon fn when the deadline comes first: it may
// linger until the mount answers, but the command pipeline is freed. The
// channel is buffered, so an abandoned fn never blocks on its send.
func detached(fn func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	return done
}

// ListStorages reports every storage (secret masked) with the apps whose
// compose files reference it; withApps false skips finding those (a read of
// every app's compose files) and reports none.
func (r *Repository) ListStorages(_ context.Context, withApps bool) ([]command.StorageInfo, error) {
	list, err := r.storages.List()
	if err != nil {
		return nil, err
	}
	var users map[string][]string
	if withApps {
		if users, err = r.storageUsers(); err != nil {
			r.log.Warn("storage list: some apps could not be checked", "error", err)
		}
	}
	out := make([]command.StorageInfo, 0, len(list))
	for _, s := range list {
		out = append(out, describeStorage(s, users[s.Name]))
	}
	return out, nil
}

// describeStorage is s as it leaves the agent: secret masked, apps a list
// even when empty.
func describeStorage(s model.Storage, apps []string) command.StorageInfo {
	if apps == nil {
		apps = []string{}
	}
	return command.StorageInfo{Storage: s.Masked(), Location: s.Location(), Apps: apps}
}

// storageUsers maps each storage name to the ids of the apps (in id order)
// whose compose files reference it or whose enabled backups use it. File
// reads only, on request — never polled. An app whose compose files can't
// be read is reported in the error while the map keeps every app read;
// callers that must not miss a user refuse on the error.
func (r *Repository) storageUsers() (map[string][]string, error) {
	users := map[string][]string{}
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if errors.Is(err, fs.ErrNotExist) {
		return users, nil
	}
	if err != nil {
		return users, err
	}
	var problems []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(r.cfg.GetAppsDataDir(), e.Name())
		files := r.appComposeFiles(dir)
		refs, err := r.storageRefs(dir, files)
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		var used []string
		for _, v := range refs {
			if ref, err := model.ParseStorageRef(v); err == nil && ref.Storage != "" {
				used = append(used, ref.Storage)
			}
		}
		cfg, err := r.appBackups(dir, files)
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
		}
		for _, b := range cfg.backups {
			if b.Enabled {
				used = append(used, b.Storage)
			}
		}
		seen := map[string]bool{}
		for _, name := range used {
			if !seen[name] {
				seen[name] = true
				users[name] = append(users[name], e.Name())
			}
		}
	}
	if len(problems) > 0 {
		return users, errors.New(strings.Join(problems, "; "))
	}
	return users, nil
}

// resolveStorage prepares a requested storage: normalized and validated, a
// local folder kept out of the agent's data dir, an S3 secret resolved
// ("<encrypted>" reuses the one stored under the same name: prev, when
// exists) and decrypted. The returned storage still carries the ciphertext,
// ready to store; secret is the plaintext, which only ever lives in memory
// here.
func (r *Repository) resolveStorage(in, prev model.Storage, exists bool) (model.Storage, string, error) {
	s := in.Normalized()
	if err := s.Validate(); err != nil {
		return model.Storage{}, "", err
	}
	if s.Type == model.StorageLocal {
		data, err := filepath.Abs(r.cfg.GetAgentDataDir())
		if err == nil && within(s.Local.Path, data) {
			return model.Storage{}, "", fmt.Errorf("the folder cannot be inside winterflow's data folder %s", data)
		}
		return s, "", nil
	}
	if s.S3.SecretAccessKey == model.EncryptedSecret {
		if !exists || prev.S3 == nil {
			return model.Storage{}, "", fmt.Errorf("enter the secret access key: %s has none stored", s.Name)
		}
		s.S3.SecretAccessKey = prev.S3.SecretAccessKey
	}
	secret, err := r.storageSecret(s)
	if err != nil {
		return model.Storage{}, "", err
	}
	return s, secret, nil
}

// storageSecret decrypts an S3 storage's secret access key with the agent's
// key.
func (r *Repository) storageSecret(s model.Storage) (string, error) {
	plain, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), s.S3.SecretAccessKey)
	if err != nil {
		return "", fmt.Errorf("decrypt the secret access key of storage %s: %w", s.Name, err)
	}
	return plain, nil
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// probeStorage opens the storage and writes and deletes a probe object
// within the probe limit, abandoning the probe on timeout (see detached).
func (r *Repository) probeStorage(parent context.Context, s model.Storage, secret string) error {
	ctx, cancel := context.WithTimeout(parent, r.storageLimits.probe)
	defer cancel()
	select {
	case err := <-detached(func() error { return r.runProbe(ctx, s, secret) }):
		return err
	case <-ctx.Done():
		if err := parent.Err(); err != nil {
			return fmt.Errorf("%s was not tested: %w", s.Name, err)
		}
		return fmt.Errorf("%s did not answer within %s", s.Name, r.storageLimits.probe)
	}
}

func (r *Repository) runProbe(ctx context.Context, s model.Storage, secret string) error {
	st, err := r.openStorage(s, secret)
	if err != nil {
		return err
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	key := ".winterflow-probe-" + hex.EncodeToString(b[:])
	if err := st.Put(ctx, key, strings.NewReader("winterflow storage probe\n")); err != nil {
		return explainWrite(s, err)
	}
	if err := st.Delete(ctx, key); err != nil {
		return fmt.Errorf("the test file was written but could not be deleted: %w", err)
	}
	return nil
}

// explainWrite turns a failed probe write into advice. A bucket addressed
// virtual-host style (bucket.endpoint) whose hostname doesn't resolve — any
// endpoint given as an IP address or localhost, and most self-hosted stores
// (MinIO, Garage) — needs path-style addressing. Outside /mnt and /media
// the systemd unit (ProtectSystem=strict) keeps a local folder read-only
// for the agent; inside them it is plain file permissions.
func explainWrite(s model.Storage, err error) error {
	var dnsErr *net.DNSError
	if s.Type == model.StorageS3 && s.S3 != nil && !s.S3.PathStyle && errors.As(err, &dnsErr) &&
		strings.HasPrefix(dnsErr.Name, s.S3.Bucket+".") {
		return fmt.Errorf("the bucket's hostname %s doesn't resolve — try path-style addressing (write a test file: %w)", dnsErr.Name, err)
	}
	if s.Type != model.StorageLocal || !(errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)) {
		return fmt.Errorf("write a test file: %w", err)
	}
	for _, root := range store.MountRoots {
		if within(s.Local.Path, root) {
			return fmt.Errorf("cannot write to %s: %w — make the folder writable by the user winterflow runs as", s.Local.Path, err)
		}
	}
	return fmt.Errorf("cannot write to %s: %w — the winterflow service may only write under /mnt and /media; "+
		"to use this folder, allow it with a systemd drop-in (see docs/recipes/backup-storage.md)", s.Local.Path, err)
}

// TestStorage checks a storage without saving it. Any problem — bad input,
// an unknown secret, a failed probe — is reported in the response, not as a
// command error, so the UI can show it inline.
func (r *Repository) TestStorage(ctx context.Context, in model.Storage) (command.TestStorageResponse, error) {
	prev, exists, err := r.storages.Get(in.Normalized().Name)
	var s model.Storage
	var secret string
	if err == nil {
		s, secret, err = r.resolveStorage(in, prev, exists)
	}
	if err == nil {
		err = r.probeStorage(ctx, s, secret)
	}
	if err != nil {
		return command.TestStorageResponse{OK: false, Error: err.Error()}, nil
	}
	return command.TestStorageResponse{OK: true}, nil
}

// SaveStorage tests a storage, stores it, and redeploys the running apps
// that reference it so they read the new values. create adds a storage and
// refuses a name the server already has (an add must never replace one,
// secret and all); otherwise it edits one and refuses an unknown name or a
// change of type. Nothing is stored when the test fails. Redeploy problems
// are warnings: the storage is saved.
func (r *Repository) SaveStorage(ctx context.Context, in model.Storage, create bool) (command.SaveStorageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, r.storageLimits.saveBudget)
	defer cancel()
	check := func(prev model.Storage, exists bool) error {
		return checkSaveMode(in.Normalized(), prev, exists, create)
	}
	// Checked before the probe (fail fast, and an edit's "<encrypted>"
	// secret must not resolve against a storage of another type) and again
	// under the registry's lock when storing.
	prev, exists, err := r.storages.Get(in.Normalized().Name)
	if err != nil {
		return command.SaveStorageResponse{}, err
	}
	if err := check(prev, exists); err != nil {
		return command.SaveStorageResponse{}, err
	}
	s, secret, err := r.resolveStorage(in, prev, exists)
	if err != nil {
		return command.SaveStorageResponse{}, err
	}
	if err := r.probeStorage(ctx, s, secret); err != nil {
		return command.SaveStorageResponse{}, fmt.Errorf("the connection test failed, nothing was saved: %w", err)
	}
	if err := r.storages.Save(s, check); err != nil {
		return command.SaveStorageResponse{}, err
	}
	users, err := r.storageUsers()
	resp := command.SaveStorageResponse{Storage: describeStorage(s, users[s.Name])}
	if err != nil {
		resp.Warnings = append(resp.Warnings, "some apps could not be checked for this storage: "+err.Error())
	}
	redeployed, warnings := r.redeployStorageUsers(ctx, users[s.Name])
	resp.Redeployed = redeployed
	resp.Warnings = append(resp.Warnings, warnings...)
	return resp, nil
}

// checkSaveMode refuses an add over an existing storage, an edit of a
// storage that doesn't exist, and an edit that changes the type: the apps
// using a storage reference its type's fields, which another type resolves
// to empty values.
func checkSaveMode(s, prev model.Storage, exists, create bool) error {
	switch {
	case create && exists:
		return fmt.Errorf("a storage named %s already exists on this server", s.Name)
	case !create && !exists:
		return fmt.Errorf("there is no storage %s on this server", s.Name)
	case !create && prev.Type != s.Type:
		return errors.New("the type of an existing storage can't change — delete it and add it again")
	}
	return nil
}

// redeployStorageUsers brings the running apps among ids up again so they
// read the new storage.env. A stopped app stays stopped (its next start
// resolves the values). An app with undeployed changes is skipped with a
// warning: bringing it up would deploy its draft. ctx carries the overall
// budget of storage.save: apps not reached before it runs out are not
// redeployed and are named in a warning.
func (r *Repository) redeployStorageUsers(ctx context.Context, ids []string) (redeployed, warnings []string) {
	redeployed = []string{}
	if len(ids) == 0 {
		return redeployed, nil
	}
	psCtx, cancel := context.WithTimeout(ctx, r.storageLimits.docker)
	running, err := r.runningProjects(psCtx)
	cancel()
	if err != nil {
		return redeployed, []string{"could not check which apps are running, so none was redeployed: " + err.Error()}
	}
	for _, id := range ids {
		if !running[projectName(id)] {
			continue
		}
		name := r.appName(r.appDataDir(id), id)
		// Checked here so a skipped app costs no lock wait, and again under
		// the lock (redeployLocked): a draft saved or a restore begun while
		// this waited must not be deployed over.
		if skip := r.redeployCheck(id, r.hasDraft(id)); skip != redeployAllowed {
			warnings = append(warnings, storageSkipWarning(name, skip))
			continue
		}
		// Starting a redeploy that the budget would cut short could leave
		// the app down half-way: skip it unless enough time remains.
		if deadline, ok := ctx.Deadline(); ctx.Err() != nil || (ok && time.Until(deadline) < r.storageLimits.redeployMinBudget) {
			warnings = append(warnings, fmt.Sprintf("%s: not redeployed: time budget exhausted — redeploy %s to use the new settings", name, name))
			continue
		}
		upCtx, cancel := context.WithTimeout(ctx, r.storageLimits.redeploy)
		skip, err := r.redeployLocked(upCtx, id)
		cancel()
		switch {
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("%s: redeploy failed: %v", name, err))
			continue
		case skip != redeployAllowed:
			warnings = append(warnings, storageSkipWarning(name, skip))
			continue
		}
		redeployed = append(redeployed, id)
	}
	return redeployed, warnings
}

// storageSkipWarning is why storage.save left a running app as it was.
func storageSkipWarning(name string, skip redeploySkip) string {
	if skip == redeployRestoring {
		return name + " is being restored and was not redeployed: deploy it afterwards to use the new storage settings"
	}
	return name + " has undeployed changes and was not redeployed: deploy it to use the new storage settings"
}

// redeployLocked is redeploy under the app's operation lock, so it never
// runs beside another mutator or a shared-variable reload of the app. Once
// the lock is held it checks again whether the app may be redeployed (a
// draft saved or a restore begun while it waited): if not, it returns the
// reason and deploys nothing. It gives up after the busy wait (the error
// says so, and the caller reports it as a warning).
func (r *Repository) redeployLocked(ctx context.Context, appID string) (redeploySkip, error) {
	unlock, err := r.lockForMutation(ctx, appID)
	if err != nil {
		return redeployAllowed, err
	}
	defer unlock()
	if skip := r.redeployCheck(appID, r.hasDraft(appID)); skip != redeployAllowed {
		return skip, nil
	}
	return redeployAllowed, r.redeploy(ctx, appID)
}

// redeploySkip is why a running app is not brought up again after a config
// change (storage.save, backup.update).
type redeploySkip int

const (
	redeployAllowed   redeploySkip = iota
	redeployRestoring              // a restore of the app runs: it owns the containers
	redeployDraft                  // undeployed changes: bringing it up would deploy them
)

// redeployCheck decides whether a running app may be brought up again after
// a config change; draft says it has undeployed changes. Both redeploying
// paths use it.
func (r *Repository) redeployCheck(appID string, draft bool) redeploySkip {
	switch {
	case r.restoreRunning(appID):
		return redeployRestoring
	case draft:
		return redeployDraft
	}
	return redeployAllowed
}

// composeProjectsUp is the set of compose projects with containers (one
// `docker ps`): the apps that are up, or crashed rather than stopped.
func (r *Repository) composeProjectsUp(ctx context.Context) (map[string]bool, error) {
	byProject, err := r.dockerPSByProject(ctx)
	if err != nil {
		return nil, err
	}
	up := make(map[string]bool, len(byProject))
	for project, containers := range byProject {
		if len(containers) > 0 {
			up[project] = true
		}
	}
	return up, nil
}

// hasDraft reports whether the app's HEAD is ahead of what runs.
func (r *Repository) hasDraft(appID string) bool {
	dir := r.appDataDir(appID)
	deployed, ok := readDeployedMark(dir)
	if !ok {
		return false
	}
	head, err := gitHead(dir)
	return err == nil && head != deployed
}

// DeleteStorage removes a storage, refusing while an app references it (the
// error names the apps). It fails closed: when an app's compose files can't
// be read, it can't tell, so it refuses too.
func (r *Repository) DeleteStorage(_ context.Context, name string) error {
	users, err := r.storageUsers()
	if err != nil {
		return fmt.Errorf("cannot remove %s: the apps using it can't be checked: %w", name, err)
	}
	if ids := users[name]; len(ids) > 0 {
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			names = append(names, r.appName(r.appDataDir(id), id))
		}
		return fmt.Errorf("%s is used by %s: remove its WF_STORAGE_ variables from those apps and turn off their backups to it first", name, strings.Join(names, ", "))
	}
	existed, err := r.storages.Delete(name)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("there is no storage %s on this server", name)
	}
	return nil
}
