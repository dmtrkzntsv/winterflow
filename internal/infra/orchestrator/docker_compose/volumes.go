package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

const (
	composeProjectKey = "com.docker.compose.project"
	anonymousVolKey   = "com.docker.volume.anonymous"
	wfProjectPrefix   = "wf-"

	// volumeOpTimeout bounds every volume exec sequence: the standalone
	// pipeline is drained by one goroutine, so a hung docker call must not
	// wedge all app management.
	volumeOpTimeout = 2 * time.Minute
)

type dockerVolume struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Labels string `json:"Labels"`
}

// parseLabels splits docker's "k=v,k=v" label string.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func parseVolumeLS(out []byte, log *logger.Logger) []command.Volume {
	vols := make([]command.Volume, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var dv dockerVolume
		if err := json.Unmarshal([]byte(line), &dv); err != nil {
			log.Warn("parse volume line", "error", err)
			continue
		}
		labels := parseLabels(dv.Labels)
		v := command.Volume{Name: dv.Name, Driver: dv.Driver, Project: labels[composeProjectKey]}
		if id, ok := strings.CutPrefix(v.Project, wfProjectPrefix); ok && id != "" {
			v.AppID = id
		}
		_, v.Anonymous = labels[anonymousVolKey]
		vols = append(vols, v)
	}
	return vols
}

// parseMountedVolumes returns the set of mount sources named in `docker ps`
// output. Bind sources (host paths) land in the set too; they never collide
// with volume names, which cannot contain '/'.
func parseMountedVolumes(out []byte) map[string]bool {
	mounted := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var c struct {
			Mounts string `json:"Mounts"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &c) != nil {
			continue
		}
		for _, m := range strings.Split(c.Mounts, ",") {
			if m = strings.TrimSpace(m); m != "" {
				mounted[m] = true
			}
		}
	}
	return mounted
}

func annotateVolumes(vols []command.Volume, mounted map[string]bool, appExists func(string) bool) {
	for i := range vols {
		vols[i].InUse = mounted[vols[i].Name]
		vols[i].Orphan = vols[i].AppID != "" && !appExists(vols[i].AppID)
	}
}

// sortVolumes: winterflow-owned (by app id, then name), other named, anonymous.
func sortVolumes(vols []command.Volume) {
	rank := func(v command.Volume) int {
		switch {
		case v.AppID != "":
			return 0
		case v.Anonymous:
			return 2
		default:
			return 1
		}
	}
	sort.SliceStable(vols, func(i, j int) bool {
		a, b := vols[i], vols[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		return a.Name < b.Name
	})
}

// ListVolumes returns every Docker volume with its owning app, whether any
// container (running or stopped) mounts it, and whether it is orphaned.
// Exactly two execs; called only on request, never polled.
func (r *Repository) ListVolumes(ctx context.Context) ([]command.Volume, error) {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	lsOut, err := exec.CommandContext(ctx, "docker", "volume", "ls", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker volume ls: %w", err)
	}
	psOut, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	vols := parseVolumeLS(lsOut, r.log)
	annotateVolumes(vols, parseMountedVolumes(psOut), r.appExists)
	sortVolumes(vols)
	return vols, nil
}

// CreateVolume creates a local Docker volume.
func (r *Repository) CreateVolume(ctx context.Context, name string) error {
	if err := command.ValidateVolumeName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "volume", "create", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteVolume removes a Docker volume. No --force: Docker refuses a volume
// in use and that reason is returned to the caller.
func (r *Repository) DeleteVolume(ctx context.Context, name string) error {
	if err := command.ValidateVolumeName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "volume", "rm", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume rm: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// composeDownVerb is the `compose down` verb; -v also removes the named
// volumes the compose file declares and anonymous volumes (never external).
func composeDownVerb(volumes bool) []string {
	if volumes {
		return []string{"down", "-v", "--remove-orphans"}
	}
	return []string{"down", "--remove-orphans"}
}

func projectLabelFilter(appID string) string {
	return "label=" + composeProjectKey + "=" + projectName(appID)
}

// sweepAppVolumes force-removes any container still labelled with the app's
// project (left behind when `compose down` failed) and then every volume
// carrying that label — including volumes earlier revisions declared. Every
// failure becomes a warning; the caller deletes the app regardless.
func (r *Repository) sweepAppVolumes(ctx context.Context, appID string) []string {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	var warnings []string
	warn := func(msg string, err error) {
		r.log.Warn("delete: "+msg, "app_id", appID, "error", err)
		warnings = append(warnings, fmt.Sprintf("%s: %v", msg, err))
	}
	filter := projectLabelFilter(appID)

	if out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", filter).Output(); err != nil {
		warn("could not list leftover containers", err)
	} else if ids := strings.Fields(string(out)); len(ids) > 0 {
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			warn("could not remove leftover containers", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
	}

	out, err := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", filter).Output()
	if err != nil {
		warn("could not list volumes", err)
		return warnings
	}
	for _, name := range strings.Fields(string(out)) {
		if out, err := exec.CommandContext(ctx, "docker", "volume", "rm", name).CombinedOutput(); err != nil {
			warn("volume "+name+" not removed", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
	}
	return warnings
}
