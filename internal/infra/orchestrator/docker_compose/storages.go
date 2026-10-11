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

// Time limits of the storage commands. Variables so tests can shrink them.
var (
	// storageProbeTimeout bounds a storage test: opening the storage and the
	// probe write and delete (storage.test, and the test storage.save runs
	// first).
	storageProbeTimeout = 30 * time.Second
	// storageRedeployTimeout bounds one app's redeploy after storage.save.
	storageRedeployTimeout = 10 * time.Minute
	// storageDockerTimeout bounds the one `docker ps` storage.save runs.
	storageDockerTimeout = 30 * time.Second
	// storageSaveBudget is the overall budget of one storage.save (probe,
	// docker ps and every redeploy share it). It stays below the dispatch
	// manager's 10 minute pending TTL, so the result is not dropped, and
	// bounds how long the command can hold the (standalone: single) pipeline.
	storageSaveBudget = 8 * time.Minute
	// storageRedeployMinBudget is the least of the save's budget that must
	// remain to start another redeploy.
	storageRedeployMinBudget = 2 * time.Minute
)

// openStorage opens a storage driver; replaced in tests.
var openStorage = store.Open

// mountRoots are the folders the systemd unit lets the agent write
// (ReadWritePaths=-/mnt -/media): the usual mount points of USB disks and
// network shares.
var mountRoots = []string{"/mnt", "/media"}

// ListStorages reports every storage (secret masked) with the apps whose
// compose files reference it.
func (r *Repository) ListStorages(context.Context) ([]command.StorageInfo, error) {
	list, err := r.storages.List()
	if err != nil {
		return nil, err
	}
	users, err := r.storageUsers()
	if err != nil {
		r.log.Warn("storage list: some apps could not be checked", "error", err)
	}
	out := make([]command.StorageInfo, 0, len(list))
	for _, s := range list {
		out = append(out, describeStorage(s, users[s.Name]))
	}
	return out, nil
}

func describeStorage(s model.Storage, apps []string) command.StorageInfo {
	return command.StorageInfo{Storage: s.Masked(), Location: s.Location(), Apps: apps}
}

// storageUsers maps each storage name to the ids of the apps (in id order)
// whose compose files reference it. File reads only, on request — never
// polled. An app whose compose files can't be read is reported in the error
// while the map keeps every app read; callers that must not miss a user
// refuse on the error.
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
		refs, err := r.storageRefs(filepath.Join(r.cfg.GetAppsDataDir(), e.Name()))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		seen := map[string]bool{}
		for _, v := range refs {
			ref, err := model.ParseStorageRef(v)
			if err != nil || seen[ref.Storage] {
				continue
			}
			seen[ref.Storage] = true
			users[ref.Storage] = append(users[ref.Storage], e.Name())
		}
	}
	if len(problems) > 0 {
		return users, errors.New(strings.Join(problems, "; "))
	}
	return users, nil
}

// resolveStorage prepares a requested storage: normalized and validated, a
// local folder kept out of the agent's data dir, an S3 secret resolved
// ("<encrypted>" reuses the one stored under the same name) and decrypted.
// The returned storage still carries the ciphertext, ready to store; secret
// is the plaintext, which only ever lives in memory here.
func (r *Repository) resolveStorage(in model.Storage) (model.Storage, string, error) {
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
		prev, ok, err := r.storages.Get(s.Name)
		if err != nil {
			return model.Storage{}, "", err
		}
		if !ok || prev.S3 == nil {
			return model.Storage{}, "", fmt.Errorf("enter the secret access key: %s has none stored", s.Name)
		}
		s.S3.SecretAccessKey = prev.S3.SecretAccessKey
	}
	secret, err := crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), s.S3.SecretAccessKey)
	if err != nil {
		return model.Storage{}, "", fmt.Errorf("decrypt the secret access key: %w", err)
	}
	return s, secret, nil
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// probeStorage opens the storage and writes and deletes a probe object
// within storageProbeTimeout. The local driver's syscalls ignore contexts (a
// hung hard NFS/CIFS mount blocks them indefinitely), so the probe runs in a
// goroutine that is abandoned on timeout: it may linger until the mount
// answers, but the command pipeline is freed.
func probeStorage(parent context.Context, s model.Storage, secret string) error {
	ctx, cancel := context.WithTimeout(parent, storageProbeTimeout)
	defer cancel()
	done := make(chan error, 1) // buffered: an abandoned probe must not block on send
	open := openStorage
	go func() { done <- runProbe(ctx, open, s, secret) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if err := parent.Err(); err != nil {
			return fmt.Errorf("%s was not tested: %w", s.Name, err)
		}
		return fmt.Errorf("%s did not answer within %s", s.Name, storageProbeTimeout)
	}
}

func runProbe(ctx context.Context, open func(model.Storage, string) (store.Store, error), s model.Storage, secret string) error {
	st, err := open(s, secret)
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
	for _, root := range mountRoots {
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
	s, secret, err := r.resolveStorage(in)
	if err == nil {
		err = probeStorage(ctx, s, secret)
	}
	if err != nil {
		return command.TestStorageResponse{OK: false, Error: err.Error()}, nil
	}
	return command.TestStorageResponse{OK: true}, nil
}

// SaveStorage tests a storage, stores it, and redeploys the running apps
// that reference it so they read the new values. Nothing is stored when the
// test fails. Redeploy problems are warnings: the storage is saved.
func (r *Repository) SaveStorage(ctx context.Context, in model.Storage) (command.SaveStorageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, storageSaveBudget)
	defer cancel()
	s, secret, err := r.resolveStorage(in)
	if err != nil {
		return command.SaveStorageResponse{}, err
	}
	if err := probeStorage(ctx, s, secret); err != nil {
		return command.SaveStorageResponse{}, fmt.Errorf("the connection test failed, nothing was saved: %w", err)
	}
	if err := r.storages.Put(s); err != nil {
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

// redeployStorageUsers brings the running apps among ids up again so they
// read the new storage.env. A stopped app stays stopped (its next start
// resolves the values). An app with undeployed changes is skipped with a
// warning: bringing it up would deploy its draft. ctx carries the overall
// budget of storage.save: apps not reached before it runs out are not
// redeployed and are named in a warning.
func (r *Repository) redeployStorageUsers(ctx context.Context, ids []string) (redeployed, warnings []string) {
	if len(ids) == 0 {
		return nil, nil
	}
	psCtx, cancel := context.WithTimeout(ctx, storageDockerTimeout)
	running, err := r.runningProjects(psCtx)
	cancel()
	if err != nil {
		return nil, []string{"could not check which apps are running, so none was redeployed: " + err.Error()}
	}
	for _, id := range ids {
		if !running[projectName(id)] {
			continue
		}
		name := r.appName(r.appDataDir(id), id)
		if r.hasDraft(id) {
			warnings = append(warnings, name+" has undeployed changes and was not redeployed: deploy it to use the new storage settings")
			continue
		}
		// Starting a redeploy that the budget would cut short could leave
		// the app down half-way: skip it unless enough time remains.
		if deadline, ok := ctx.Deadline(); ctx.Err() != nil || (ok && time.Until(deadline) < storageRedeployMinBudget) {
			warnings = append(warnings, fmt.Sprintf("%s: not redeployed: time budget exhausted — redeploy %s to use the new settings", name, name))
			continue
		}
		upCtx, cancel := context.WithTimeout(ctx, storageRedeployTimeout)
		err := r.redeploy(upCtx, id)
		cancel()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: redeploy failed: %v", name, err))
			continue
		}
		redeployed = append(redeployed, id)
	}
	return redeployed, warnings
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
		return fmt.Errorf("%s is used by %s: remove its WF_STORAGE_ variables from those apps first", name, strings.Join(names, ", "))
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
