package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

const (
	anonymousVolKey = "com.docker.volume.anonymous"
	wfProjectPrefix = "wf-"

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
		v := command.Volume{
			Name:      dv.Name,
			Driver:    dv.Driver,
			Project:   labelValue(dv.Labels, composeProjectLabel),
			Anonymous: hasLabel(dv.Labels, anonymousVolKey),
		}
		if id, ok := strings.CutPrefix(v.Project, wfProjectPrefix); ok && id != "" {
			v.AppID = id
		}
		vols = append(vols, v)
	}
	return vols
}

// annotateVolumes marks usage and orphanhood. A volume is in use unless
// Docker reports it dangling — referenced by no container, running or
// stopped.
func annotateVolumes(vols []command.Volume, dangling map[string]bool, appExists func(string) bool) {
	for i := range vols {
		vols[i].InUse = !dangling[vols[i].Name]
		vols[i].Orphan = vols[i].AppID != "" && !appExists(vols[i].AppID)
	}
}

// sortVolumes orders by name; grouping is the UI's job.
func sortVolumes(vols []command.Volume) {
	slices.SortFunc(vols, func(a, b command.Volume) int { return strings.Compare(a.Name, b.Name) })
}

// ListVolumes returns every Docker volume with its owning app, whether any
// container (running or stopped) mounts it, and whether it is orphaned.
// Exactly two concurrent execs; called only on request, never polled.
func (r *Repository) ListVolumes(ctx context.Context) ([]command.Volume, error) {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	var (
		wg                 sync.WaitGroup
		lsOut, danglingOut []byte
		lsErr, danglingErr error
	)
	wg.Go(func() { lsOut, lsErr = execOutput(ctx, "docker", "volume", "ls", "--format", "json") })
	wg.Go(func() {
		danglingOut, danglingErr = execOutput(ctx, "docker", "volume", "ls", "-q", "--filter", "dangling=true")
	})
	wg.Wait()
	if lsErr != nil {
		return nil, fmt.Errorf("docker volume ls: %w", lsErr)
	}
	if danglingErr != nil {
		return nil, fmt.Errorf("docker volume ls dangling: %w", danglingErr)
	}
	dangling := map[string]bool{}
	for _, name := range strings.Fields(string(danglingOut)) {
		dangling[name] = true
	}
	vols := parseVolumeLS(lsOut, r.log)
	annotateVolumes(vols, dangling, r.appExists)
	sortVolumes(vols)
	return vols, nil
}

// CreateVolume creates a local Docker volume. The name was validated by the
// dispatcher (command.ValidateVolumeName), so it cannot be parsed as a flag.
func (r *Repository) CreateVolume(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	return dockerCombined(ctx, "volume", "create", name)
}

// DeleteVolume removes a Docker volume. No --force: Docker refuses a volume
// in use and that reason is returned to the caller. The name was validated by
// the dispatcher (command.ValidateVolumeName).
func (r *Repository) DeleteVolume(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	return dockerCombined(ctx, "volume", "rm", name)
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
	return "label=" + composeProjectLabel + "=" + projectName(appID)
}

// sweepAppVolumes removes every volume carrying the app's compose project
// label — including volumes earlier revisions declared — in one `docker
// volume rm`. Only when `compose down` failed (downFailed) can containers
// still hold them, so only then are leftover project containers force-removed
// first. Every failure becomes a warning; the caller deletes the app
// regardless.
func (r *Repository) sweepAppVolumes(ctx context.Context, appID string, downFailed bool) []string {
	ctx, cancel := context.WithTimeout(ctx, volumeOpTimeout)
	defer cancel()
	var warnings []string
	warn := func(msg string, err error) {
		r.log.Warn("delete: "+msg, "app_id", appID, "error", err)
		warnings = append(warnings, fmt.Sprintf("%s: %v", msg, err))
	}
	filter := projectLabelFilter(appID)

	if downFailed {
		if out, err := execOutput(ctx, "docker", "ps", "-aq", "--filter", filter); err != nil {
			warn("could not list leftover containers", err)
		} else if ids := strings.Fields(string(out)); len(ids) > 0 {
			if err := dockerCombined(ctx, append([]string{"rm", "-f"}, ids...)...); err != nil {
				warn("could not remove leftover containers", err)
			}
		}
	}

	out, err := execOutput(ctx, "docker", "volume", "ls", "-q", "--filter", filter)
	if err != nil {
		warn("could not list volumes", err)
		return warnings
	}
	if names := strings.Fields(string(out)); len(names) > 0 {
		if err := dockerCombined(ctx, append([]string{"volume", "rm"}, names...)...); err != nil {
			warn("could not remove volumes", err)
		}
	}
	return warnings
}
