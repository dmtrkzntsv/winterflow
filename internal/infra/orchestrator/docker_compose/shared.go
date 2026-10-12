package dockercompose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/varref"
	agentvars "winterflow/internal/infra/vars"
	"winterflow/pkg/crypto"
)

// refEntry is one app's cached referenced-name set.
type refEntry struct {
	key  string
	refs map[string]bool
}

// hostTimezone names the host's IANA zone for WF_TZ: /etc/timezone, else
// the /etc/localtime link target, else "UTC". ($TZ is deliberately not
// read: no new env var.)
func hostTimezone() string {
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(raw)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			return target[i+len("zoneinfo/"):]
		}
	}
	return "UTC"
}

// refKey identifies the inputs of a reference scan: HEAD (compose file,
// .env and secrets.json are committed) plus the pinned upstream SHA (a
// git-sourced app's compose file lives in the uncommitted source/).
func refKey(dir string) (string, error) {
	head, err := gitHead(dir)
	if err != nil {
		return "", err
	}
	lock, _ := readSourceLock(dir)
	return head + "|" + lock.SHA, nil
}

// appRefs returns the shared names appID references: names used in the
// compose file it runs or in its committed .env, minus the names it
// defines itself (its own value wins anyway). Cached by refKey; a folder
// that is not a repository (yet) is scanned uncached.
func (r *Repository) appRefs(appID string) map[string]bool {
	dir := r.appDataDir(appID)
	key, err := refKey(dir)
	if err == nil {
		r.refMu.Lock()
		e, ok := r.refCache[appID]
		r.refMu.Unlock()
		if ok && e.key == key {
			return e.refs
		}
	}
	refs := r.scanRefs(dir)
	if err == nil {
		r.refMu.Lock()
		r.refCache[appID] = refEntry{key: key, refs: refs}
		r.refMu.Unlock()
	}
	return refs
}

func (r *Repository) scanRefs(dir string) map[string]bool {
	refs := map[string]bool{}
	// The compose files every invocation passes explicitly (override
	// included), plus the committed .env.
	files := append([]string{envRel}, r.appComposeFiles(dir)...)
	for _, f := range files {
		if raw, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			varref.References(raw, refs)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, envRel)); err == nil {
		for name := range parseEnv(raw) {
			delete(refs, name)
		}
	}
	for name := range loadSecretStore(dir).Variables {
		delete(refs, name)
	}
	return refs
}

// sharedResolution resolves the current snapshot with this server's
// built-ins, decrypting secrets with the agent key.
func (r *Repository) sharedResolution() agentvars.Resolution {
	snap := r.vars.Current()
	dataDir, err := filepath.Abs(r.cfg.GetAgentDataDir())
	if err != nil {
		dataDir = r.cfg.GetAgentDataDir()
	}
	return agentvars.Resolve(snap, agentvars.ServerBuiltins(snap, dataDir, r.tz), func(ct string) (string, error) {
		return crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), ct)
	})
}

// desiredShared is what appID's two shared files should hold under res:
// every built-in, plus each referenced name res defines, split by secrecy.
func (r *Repository) desiredShared(appID string, res agentvars.Resolution, refs map[string]bool) (plain, secret map[string]string) {
	dir := r.appDataDir(appID)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	plain, secret = map[string]string{}, map[string]string{}
	for name, v := range res.Values {
		if v.Scope == agentvars.ScopeBuiltin {
			plain[name] = v.Value
		}
	}
	for name, v := range agentvars.AppBuiltins(appID, slugify(r.appName(dir, appID)), absDir) {
		plain[name] = v
	}
	for name := range refs {
		v, ok := res.Values[name]
		if !ok || v.Scope == agentvars.ScopeBuiltin {
			continue
		}
		if v.Secret {
			secret[name] = v.Value
		} else {
			plain[name] = v.Value
		}
	}
	return plain, secret
}

// materializeForDeploy prepares dir for a deploy: the app's own secrets,
// then the shared variables. Only deploying paths call it (save, start,
// update, rollback, source auto-update) — a draft save materializes its own
// secrets but leaves the shared files, and with them the stale flag, alone.
func (r *Repository) materializeForDeploy(dir string, s secretStore) error {
	if err := r.materializeSecrets(dir, s); err != nil {
		return fmt.Errorf("materialize secrets: %w", err)
	}
	if err := r.renderShared(filepath.Base(dir)); err != nil {
		return fmt.Errorf("render shared variables: %w", err)
	}
	return nil
}

// renderShared writes appID's .env.shared (0644) and .env.shared.secrets
// (0600, removed when empty) from the current snapshot and marks the app
// fresh. Referenced secrets this server has no usable copy of are logged
// by name only; compose then sees them unset (or their default).
func (r *Repository) renderShared(appID string) error {
	r.sharedMu.Lock()
	defer r.sharedMu.Unlock()
	dir := r.appDataDir(appID)
	refs := r.appRefs(appID)
	res := r.sharedResolution()
	plain, secret := r.desiredShared(appID, res, refs)
	for _, ref := range append(append([]command.VarRef{}, res.Missing...), res.Undecryptable...) {
		if _, resolved := res.Values[ref.Name]; refs[ref.Name] && !resolved {
			r.log.Warn("shared secret not available on this server; compose sees it unset",
				"app_id", appID, "name", ref.Name, "scope", ref.Scope)
		}
	}
	if err := ensureIgnored(dir); err != nil {
		return fmt.Errorf("guard .gitignore: %w", err)
	}
	if err := writeSharedEnv(filepath.Join(dir, envSharedRel), plain, 0o644); err != nil {
		return err
	}
	secretsPath := filepath.Join(dir, envSharedSecretsRel)
	if len(secret) == 0 {
		if err := os.Remove(secretsPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := writeSharedEnv(secretsPath, secret, 0o600); err != nil {
		return err
	}
	r.setStale(appID, nil)
	return nil
}

func writeSharedEnv(path string, values map[string]string, perm os.FileMode) error {
	items := make([]resolvedItem, 0, len(values))
	for name, v := range values {
		items = append(items, resolvedItem{name: name, content: []byte(v)})
	}
	if err := os.WriteFile(path, marshalSharedEnv(items), perm); err != nil {
		return err
	}
	return os.Chmod(path, perm) // WriteFile's perm only applies on create
}

// staleNames compares appID's rendered shared files with what res would
// render, for the names the app references only — so an unreferenced
// change, a re-encrypted secret or a never-rendered app that references
// nothing stays fresh. Secrets are compared in memory, never hashed to disk.
func (r *Repository) staleNames(appID string, res agentvars.Resolution) []string {
	dir := r.appDataDir(appID)
	refs := r.appRefs(appID)
	plain, secret := r.desiredShared(appID, res, refs)
	onDisk := map[string]string{}
	for _, rel := range []string{envSharedRel, envSharedSecretsRel} {
		if raw, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			for k, v := range parseEnv(raw) {
				onDisk[k] = v
			}
		}
	}
	var names []string
	for name := range refs {
		want, inWant := plain[name]
		if !inWant {
			want, inWant = secret[name]
		}
		have, inHave := onDisk[name]
		if inWant != inHave || want != have {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Repository) setStale(appID string, names []string) {
	r.staleMu.Lock()
	defer r.staleMu.Unlock()
	if len(names) == 0 {
		delete(r.stale, appID)
		return
	}
	r.stale[appID] = names
}

// RecomputeStale rebuilds the stale set for every app from the current
// snapshot. File reads only: no process is forked and reference scans are
// cached by HEAD. Holds sharedMu throughout so it never interleaves with a
// render.
func (r *Repository) RecomputeStale() {
	r.sharedMu.Lock()
	defer r.sharedMu.Unlock()
	next := map[string][]string{}
	if entries, err := os.ReadDir(r.cfg.GetAppsDataDir()); err == nil {
		res := r.sharedResolution()
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if names := r.staleNames(e.Name(), res); len(names) > 0 {
				next[e.Name()] = names
			}
		}
	}
	r.staleMu.Lock()
	r.stale = next
	r.staleReady = true
	r.staleMu.Unlock()
}

// StaleApps returns a copy of the stale set (app id -> stale names),
// computing it on first use after a start.
func (r *Repository) StaleApps() map[string][]string {
	r.staleMu.Lock()
	ready := r.staleReady
	r.staleMu.Unlock()
	if !ready {
		r.RecomputeStale()
	}
	r.staleMu.Lock()
	defer r.staleMu.Unlock()
	out := make(map[string][]string, len(r.stale))
	for k, v := range r.stale {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// withStale attaches each app's stale names to a status report.
func (r *Repository) withStale(apps []command.AppStatus) []command.AppStatus {
	stale := r.StaleApps()
	for i := range apps {
		apps[i].StaleVars = stale[apps[i].AppID]
	}
	return apps
}

// SyncVars stores a vars.sync snapshot (an older revision is ignored),
// reports the secrets it cannot decrypt and recomputes which apps are
// stale. It renders nothing: running apps keep their values until a
// deploy or a vars.reload.
func (r *Repository) SyncVars(_ context.Context, in command.SyncVarsRequest) (command.SyncVarsResponse, error) {
	applied, err := r.vars.Apply(in)
	if err != nil {
		return command.SyncVarsResponse{}, fmt.Errorf("store shared variables: %w", err)
	}
	if !applied {
		r.log.Info("ignored an older shared-variables snapshot", "revision", in.Revision, "stored", r.vars.Current().Revision)
	}
	undecryptable := r.sharedResolution().Undecryptable
	if undecryptable == nil {
		undecryptable = []command.VarRef{}
	}
	r.RecomputeStale()
	return command.SyncVarsResponse{Revision: r.vars.Current().Revision, Undecryptable: undecryptable}, nil
}

// VarsRevision is the stored snapshot's revision (0 before the first sync).
func (r *Repository) VarsRevision() int64 { return r.vars.Current().Revision }
