package dockercompose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
)

// composeConfigTimeout bounds one `docker compose config`.
const composeConfigTimeout = 30 * time.Second

// composeModel is what backups read of `docker compose config --format
// json`: compose has resolved every name (the project prefix, name:,
// external:) and made relative bind sources absolute.
type composeModel struct {
	Services map[string]struct {
		Volumes []composeMount `json:"volumes"`
	} `json:"services"`
	Volumes map[string]struct {
		Name string `json:"name"`
	} `json:"volumes"`
}

// composeMount is one entry of a service's volumes.
type composeMount struct {
	Type   string `json:"type"` // volume | bind | tmpfs | ...
	Source string `json:"source"`
	Target string `json:"target"`
}

// helperMount is one source the helper container mounts, at /src/<n>: a
// Docker volume or an absolute host path, both already mounted by the app.
type helperMount struct {
	Volume string
	Bind   string
}

// helperPath is one path of a backup in the helper: Sub below mount Mount
// ("" = the mount itself), archived as Entry ("" = the archive root).
type helperPath struct {
	Mount int
	Sub   string
	Entry string
}

// Src is the path in the helper.
func (p helperPath) Src() string {
	src := "/src/" + strconv.Itoa(p.Mount)
	if p.Sub != "" {
		src += "/" + p.Sub
	}
	return src
}

// helperPlan is how the helper reaches a backup's data.
type helperPlan struct {
	Mounts []helperMount
	Paths  []helperPath
	// Warnings are mounts left out of the copy (a nested tmpfs, a
	// single-file bind): the run reports them.
	Warnings []string
}

// mountCacheEntry is an app's composeModel, valid while key (HEAD and the
// derived env files) is unchanged.
type mountCacheEntry struct {
	key   string
	model composeModel
}

// resolveBackup maps a sqlite or files backup's paths to the sources the
// helper mounts. isFile reports whether a host path is a regular file (a
// single-file bind mount).
func resolveBackup(m composeModel, b recipe.EffectiveBackup, isFile func(string) bool) (helperPlan, error) {
	var plan helperPlan
	if b.Volume != "" {
		v, ok := m.Volumes[b.Volume]
		if !ok || v.Name == "" {
			return helperPlan{}, fmt.Errorf("backup %s: compose has no volume %q", b.Name, b.Volume)
		}
		plan.Mounts = []helperMount{{Volume: v.Name}}
		for _, p := range backupPaths(b) {
			plan.Paths = append(plan.Paths, helperPath{Sub: p, Entry: p})
		}
		if len(plan.Paths) == 0 { // the whole volume
			plan.Paths = []helperPath{{}}
		}
		return plan, nil
	}
	svc, ok := m.Services[b.Service]
	if !ok {
		return helperPlan{}, fmt.Errorf("backup %s: compose has no service %q", b.Name, b.Service)
	}
	index := map[helperMount]int{}
	mountIndex := func(hm helperMount) int {
		n, seen := index[hm]
		if !seen {
			n = len(plan.Mounts)
			index[hm] = n
			plan.Mounts = append(plan.Mounts, hm)
		}
		return n
	}
	listed := map[string]bool{}
	for _, p := range backupPaths(b) {
		listed[p] = true
	}
	added := map[string]bool{} // nested mounts already in the plan
	for _, p := range backupPaths(b) {
		mnt, ok := mountOf(svc.Volumes, p)
		if !ok {
			return helperPlan{}, fmt.Errorf("backup %s: %s is not on a mount of service %s: the container's own files are lost whenever it is recreated, so mount a volume or a folder there", b.Name, p, b.Service)
		}
		hm, err := classifyMount(m, b, p, mnt, isFile)
		if err != nil {
			return helperPlan{}, err
		}
		sub := strings.TrimPrefix(strings.TrimPrefix(p, mnt.Target), "/")
		if b.Type == recipe.TypeSQLite && sub == "" {
			return helperPlan{}, fmt.Errorf("backup %s: %s is the mount itself, not the database file inside it", b.Name, p)
		}
		plan.Paths = append(plan.Paths, helperPath{Mount: mountIndex(hm), Sub: sub, Entry: strings.TrimPrefix(p, "/")})
		if b.Type != recipe.TypeFiles {
			continue
		}
		// A mount nested below p is a separate source: archiving p through
		// its own mount would leave the nested mount's data out.
		for _, nm := range svc.Volumes {
			t := path.Clean(nm.Target)
			if !strings.HasPrefix(t, "/") || t == p || listed[t] || added[t] ||
				!(p == "/" || strings.HasPrefix(t, p+"/")) {
				continue
			}
			added[t] = true
			nested := nm
			nested.Target = t
			nhm, err := classifyMount(m, b, t, nested, isFile)
			var skip skippedMount
			switch {
			case errors.As(err, &skip):
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: skipped, it lies below %s", skip.Error(), p))
				continue
			case err != nil:
				return helperPlan{}, err
			}
			plan.Paths = append(plan.Paths, helperPath{Mount: mountIndex(nhm), Entry: strings.TrimPrefix(t, "/")})
		}
	}
	return plan, nil
}

// skippedMount is the error of a mount a nested path may leave out (it
// holds nothing to back up, or can't be replaced); a path the user listed
// is refused with it.
type skippedMount struct{ msg string }

func (e skippedMount) Error() string { return e.msg }

// classifyMount maps the service mount mnt holding the container path p to
// the source the helper mounts.
func classifyMount(m composeModel, b recipe.EffectiveBackup, p string, mnt composeMount, isFile func(string) bool) (helperMount, error) {
	switch mnt.Type {
	case "volume":
		v, ok := m.Volumes[mnt.Source]
		if mnt.Source == "" || !ok || v.Name == "" {
			return helperMount{}, skippedMount{fmt.Sprintf("backup %s: %s is on an anonymous volume of service %s, which can't be backed up: give the volume a name", b.Name, p, b.Service)}
		}
		return helperMount{Volume: v.Name}, nil
	case "bind":
		if isFile(mnt.Source) {
			return helperMount{}, skippedMount{fmt.Sprintf("backup %s: %s is a single-file bind mount of service %s (%s), which can't be replaced from another container: mount its directory instead", b.Name, p, b.Service, mnt.Source)}
		}
		return helperMount{Bind: mnt.Source}, nil
	case "tmpfs":
		return helperMount{}, skippedMount{fmt.Sprintf("backup %s: %s is on a tmpfs mount of service %s, which keeps nothing to back up", b.Name, p, b.Service)}
	default:
		return helperMount{}, fmt.Errorf("backup %s: %s is on a %s mount of service %s, which can't be backed up", b.Name, p, mnt.Type, b.Service)
	}
}

// backupPaths is a backup's path (sqlite) or paths (files).
func backupPaths(b recipe.EffectiveBackup) []string {
	if b.Type == recipe.TypeSQLite {
		return []string{b.Path}
	}
	return b.Paths
}

// mountOf finds the mount holding the container path p: the one with the
// longest target that is p or a folder above it.
func mountOf(mounts []composeMount, p string) (composeMount, bool) {
	var best composeMount
	found := false
	for _, m := range mounts {
		t := path.Clean(m.Target)
		if !strings.HasPrefix(t, "/") {
			continue
		}
		if (t == p || t == "/" || strings.HasPrefix(p, t+"/")) && (!found || len(t) > len(best.Target)) {
			best, found = m, true
			best.Target = t
		}
	}
	return best, found
}

// appComposeModel is the app's composeModel, from one `docker compose
// config` (every profile active, so a profiled service's mounts resolve
// too), cached while HEAD and the derived env files are unchanged: it runs
// at deploy, at a backups update and, after a restart, at the first backup
// run — never on a timer.
func (r *Repository) appComposeModel(ctx context.Context, appID string, files []string) (composeModel, error) {
	dir := r.appDataDir(appID)
	key := r.composeModelKey(dir)
	r.mountMu.Lock()
	e, ok := r.mountCache[appID]
	r.mountMu.Unlock()
	if ok && key != "" && e.key == key {
		return e.model, nil
	}
	ctx, cancel := context.WithTimeout(ctx, composeConfigTimeout)
	defer cancel()
	out, err := r.composeOutput(ctx, dir, r.composeArgsFor(appID, files, []string{"*"}, "config", "--format", "json"))
	if err != nil {
		return composeModel{}, fmt.Errorf("docker compose config: %w", err)
	}
	var m composeModel
	if err := json.Unmarshal(out, &m); err != nil {
		return composeModel{}, fmt.Errorf("docker compose config: %w", err)
	}
	if key != "" {
		r.mountMu.Lock()
		r.mountCache[appID] = mountCacheEntry{key: key, model: m}
		r.mountMu.Unlock()
	}
	return m, nil
}

// composeModelKey changes whenever what compose config reads may have:
// the commit and the derived env files. "" when there is no commit.
func (r *Repository) composeModelKey(dir string) string {
	head, err := gitHead(dir)
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(head))
	for _, rel := range []string{storageEnvRel, envSecretsRel} {
		raw, _ := os.ReadFile(filepath.Join(dir, rel))
		h.Write([]byte{0})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// resolveBackups maps every enabled sqlite and files backup of the app to
// its helper plan. Without such a backup it runs nothing.
func (r *Repository) resolveBackups(ctx context.Context, appID string, files []string, backups []recipe.EffectiveBackup) (map[string]helperPlan, error) {
	var enabled []recipe.EffectiveBackup
	for _, b := range backups {
		if b.Enabled && b.Builtin() {
			enabled = append(enabled, b)
		}
	}
	out := map[string]helperPlan{}
	if len(enabled) == 0 {
		return out, nil
	}
	m, err := r.appComposeModel(ctx, appID, files)
	if err != nil {
		return nil, err
	}
	// A first pass learns which bind sources the plans look at, so only
	// those are stat'ed (bounded: they may sit on a dead share).
	touched := map[string]bool{}
	for _, b := range enabled {
		_, _ = resolveBackup(m, b, func(p string) bool { touched[p] = true; return false })
	}
	isFile, err := r.statRegularFiles(ctx, touched)
	if err != nil {
		return nil, err
	}
	for _, b := range enabled {
		plan, err := resolveBackup(m, b, func(p string) bool { return isFile[p] })
		if err != nil {
			return nil, err
		}
		out[b.Name] = plan
	}
	return out, nil
}

// statCall is one host stat in flight; regular is set before done closes.
type statCall struct {
	done    chan struct{}
	regular bool
}

// statStart starts stat'ing p unless a stat of it is still running (a
// hung share), whose call it returns instead: abandoned stats are bounded
// to one goroutine per path, not one per resolution.
func (r *Repository) statStart(p string) *statCall {
	r.statMu.Lock()
	defer r.statMu.Unlock()
	if c, ok := r.statFlight[p]; ok {
		return c
	}
	if r.statFlight == nil {
		r.statFlight = map[string]*statCall{}
	}
	c := &statCall{done: make(chan struct{})}
	r.statFlight[p] = c
	go func() {
		info, err := r.hostStat(p)
		c.regular = err == nil && info.Mode().IsRegular()
		r.statMu.Lock()
		delete(r.statFlight, p)
		r.statMu.Unlock()
		close(c.done)
	}()
	return c
}

// statRegularFiles reports which of the host paths are regular files
// (anything it can't stat counts as a folder). The paths are stat'ed
// concurrently, in goroutines abandoned (see statStart) after one shared
// folder limit: a hung NFS/CIFS mount must not wedge the (standalone:
// single) command pipeline.
func (r *Repository) statRegularFiles(parent context.Context, paths map[string]bool) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	list := make([]string, 0, len(paths))
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)
	ctx, cancel := context.WithTimeout(parent, r.storageLimits.folder)
	defer cancel()
	calls := make([]*statCall, len(list))
	for i, p := range list {
		calls[i] = r.statStart(p)
	}
	out := make(map[string]bool, len(list))
	for i, p := range list {
		select {
		case <-calls[i].done:
			out[p] = calls[i].regular
		case <-ctx.Done():
			if err := parent.Err(); err != nil {
				return nil, fmt.Errorf("the folder %s was not checked: %w", p, err)
			}
			return nil, fmt.Errorf("the folder %s did not answer within %s (is the share reachable?)", p, r.storageLimits.folder)
		}
	}
	return out, nil
}

// dockerOutputIn runs docker with args in dir and returns stdout; the error
// carries stderr.
func dockerOutputIn(ctx context.Context, dir string, args []string) ([]byte, error) {
	cmd := dockerCommand(ctx, dir, args)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}
