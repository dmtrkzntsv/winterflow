package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/metrics"
)

// diskScanTimeout bounds the hourly scan (a var so tests can shorten it). `docker system df -v` walks every
// volume (43 s measured on a dev box); a hung daemon must not stall the
// metrics reporter forever.
var diskScanTimeout = 5 * time.Minute

// systemDF mirrors `docker system df -v --format json` (one JSON object).
// Docker renders every size as a human string ("26.59MB", "0B", "N/A").
type systemDF struct {
	Images []struct {
		ID         string `json:"ID"`
		Repository string `json:"Repository"`
		Tag        string `json:"Tag"`
		Containers string `json:"Containers"`
		Size       string `json:"Size"`
		SharedSize string `json:"SharedSize"`
		UniqueSize string `json:"UniqueSize"`
	} `json:"Images"`
	Containers []struct {
		ID     string `json:"ID"`
		Image  string `json:"Image"`
		Labels string `json:"Labels"`
		Mounts string `json:"Mounts"`
		Size   string `json:"Size"`
	} `json:"Containers"`
	Volumes []struct {
		Name   string `json:"Name"`
		Labels string `json:"Labels"`
		Links  string `json:"Links"`
		Size   string `json:"Size"`
	} `json:"Volumes"`
	BuildCache []struct {
		Size   string `json:"Size"`
		Shared string `json:"Shared"`
		InUse  string `json:"InUse"`
	} `json:"BuildCache"`
}

// sizeUnits are docker's (go-units HumanSize) decimal units.
var sizeUnits = map[string]float64{
	"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "pb": 1e15,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
}

// parseHumanSize turns "1.2GB", "4.1kB", "0B" or "26.38GB (74%)" into bytes.
// "N/A", "" and anything unparsable are 0 with ok=false: an unknown size must
// not fail the scan.
func parseHumanSize(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, false
	}
	unit := strings.ToLower(s[i:])
	if unit == "" {
		unit = "b"
	}
	mult, ok := sizeUnits[unit]
	if !ok {
		return 0, false
	}
	return n * mult, true
}

func size(s string) float64 {
	v, _ := parseHumanSize(s)
	return v
}

// appOfProject maps a compose project label to the app id it belongs to.
func appOfProject(project string) string {
	id, ok := strings.CutPrefix(project, wfProjectPrefix)
	if !ok {
		return ""
	}
	return id
}

// imageKey normalizes an image reference for matching a container's Image
// against the image list ("nginx" means "nginx:latest").
func imageKey(ref string) string {
	if strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
		return ref
	}
	last := ref[strings.LastIndexByte(ref, '/')+1:]
	if !strings.Contains(last, ":") {
		return ref + ":latest"
	}
	return ref
}

// buildDiskScan derives host totals and per-app usage from one system df.
// A volume belongs to an app when its compose project is wf-<appID>, else
// when exactly one app's containers mount it (an external volume from the
// volume registry). appDirs adds each app directory's size.
func buildDiskScan(df systemDF, appDirs map[string]float64) metrics.DiskScan {
	scan := metrics.DiskScan{Apps: map[string]metrics.AppDisk{}}
	add := func(app string, disk, image float64) {
		a := scan.Apps[app]
		a.Disk += disk
		a.Image += image
		scan.Apps[app] = a
	}
	for app, dir := range appDirs {
		add(app, dir, 0)
	}

	// Images: unique bytes plus each distinct shared size once (docker's
	// exact layer dedup is not in the -v output; this lands within a few
	// percent of `docker system df`). Unused images' unique bytes are
	// reclaimable.
	imageSize := map[string]float64{}
	shared := map[float64]bool{}
	for _, im := range df.Images {
		u := size(im.UniqueSize)
		scan.Docker.Images += u
		if sh := size(im.SharedSize); sh > 0 && !shared[sh] {
			shared[sh] = true
			scan.Docker.Images += sh
		}
		if strings.TrimSpace(im.Containers) == "0" {
			scan.Docker.Reclaimable += u
		}
		full := size(im.Size)
		imageSize[im.ID] = full
		if im.Repository != "" && im.Repository != "<none>" {
			imageSize[imageKey(im.Repository+":"+im.Tag)] = full
		}
	}

	// Containers: writable layers, image per app (each image once), and
	// which apps mount which volume.
	mountedBy := map[string]map[string]bool{}
	appImages := map[string]map[string]bool{}
	for _, c := range df.Containers {
		app := appOfProject(labelValue(c.Labels, composeProjectLabel))
		for _, m := range strings.Split(c.Mounts, ",") {
			if m = strings.TrimSpace(m); m == "" || app == "" {
				continue
			}
			if mountedBy[m] == nil {
				mountedBy[m] = map[string]bool{}
			}
			mountedBy[m][app] = true
		}
		if app == "" {
			continue
		}
		add(app, size(c.Size), 0)
		key := imageKey(c.Image)
		if appImages[app] == nil {
			appImages[app] = map[string]bool{}
		}
		if !appImages[app][key] {
			appImages[app][key] = true
			add(app, 0, imageSize[key])
		}
	}

	for _, v := range df.Volumes {
		s := size(v.Size)
		scan.Docker.Volumes += s
		if strings.TrimSpace(v.Links) == "0" {
			scan.Docker.Reclaimable += s
		}
		app := appOfProject(labelValue(v.Labels, composeProjectLabel))
		if app == "" && len(mountedBy[v.Name]) == 1 {
			for only := range mountedBy[v.Name] {
				app = only
			}
		}
		if app != "" {
			add(app, s, 0)
		}
	}

	for _, b := range df.BuildCache {
		s := size(b.Size)
		scan.Docker.BuildCache += s
		if b.Shared != "true" && b.InUse != "true" {
			scan.Docker.Reclaimable += s
		}
	}
	return scan
}

// dirSize sums the regular files under dir without following symlinks.
// Unreadable subtrees (root-owned bind-mount data) are skipped, not fatal.
func dirSize(ctx context.Context, dir string) float64 {
	var total float64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += float64(info.Size())
		}
		return nil
	})
	return total
}

// ScanDisk measures disk usage: one `docker system df -v` (bounded by
// diskScanTimeout) for images, volumes, build cache and writable layers,
// plus a walk of every app directory. It is hourly work; never call it from
// the 60 s path.
func (r *Repository) ScanDisk(ctx context.Context) (metrics.DiskScan, error) {
	ctx, cancel := context.WithTimeout(ctx, diskScanTimeout)
	defer cancel()
	out, err := execOutput(ctx, "docker", "system", "df", "-v", "--format", "json")
	if err != nil {
		return metrics.DiskScan{}, err
	}
	var df systemDF
	if err := json.Unmarshal(out, &df); err != nil {
		return metrics.DiskScan{}, err
	}
	dirs := map[string]float64{}
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return metrics.DiskScan{}, err
	}
	for _, e := range entries {
		if e.IsDir() {
			dirs[e.Name()] = dirSize(ctx, r.appDataDir(e.Name()))
		}
	}
	if err := ctx.Err(); err != nil {
		return metrics.DiskScan{}, err
	}
	return buildDiskScan(df, dirs), nil
}
