// Package layout is how backups are stored in a storage: one folder per
// app backup (<app-id>/<backup>/, below the storage's own folder or prefix),
// one data object per restore point (<yyyymmddThhmmssZ>-<kind>.<db|tar>.zst)
// and its manifest beside it (<id>.json), written last. It pairs listed
// objects into points and decides which points retention deletes. Backup
// runs use it to write, and restore (C2b) to read.
package layout

import (
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/store"
)

// timeFormat is the UTC timestamp a point id starts with.
const timeFormat = "20060102T150405Z"

// ManifestExt ends a manifest's key.
const ManifestExt = ".json"

// IncompleteAfter is how long a point without a manifest (a run that died
// mid-upload) is left alone before retention deletes it.
const IncompleteAfter = 24 * time.Hour

// PreRestoreKeep is how long a pre-restore point is kept.
const PreRestoreKeep = 30 * 24 * time.Hour

// Folder is the folder of an app's backup, relative to the storage.
func Folder(appID, backup string) string { return appID + "/" + backup + "/" }

// DataExt is the data object's extension for a backup type.
func DataExt(t recipe.Type) string {
	if t == recipe.TypeSQLite {
		return ".db.zst"
	}
	return ".tar.zst"
}

// PointID names the point made at t (UTC, to the second) of kind.
func PointID(t time.Time, kind command.PointKind) string {
	return t.UTC().Format(timeFormat) + "-" + string(kind)
}

// ParsePointID splits a point id; ok is false for anything else.
func ParsePointID(id string) (time.Time, command.PointKind, bool) {
	if !command.ValidPointID(id) {
		return time.Time{}, "", false
	}
	t, err := time.Parse(timeFormat, id[:16])
	if err != nil {
		return time.Time{}, "", false
	}
	return t, command.PointKind(id[17:]), true
}

// DataKey is the key of a point's data object.
func DataKey(appID, backup, id string, t recipe.Type) string {
	return Folder(appID, backup) + id + DataExt(t)
}

// ManifestKey is the key of a point's manifest.
func ManifestKey(appID, backup, id string) string {
	return Folder(appID, backup) + id + ManifestExt
}

// Manifest describes a point's data object; a point is complete once its
// manifest exists. It never holds a secret.
type Manifest struct {
	AppID        string            `json:"app_id"`
	Backup       string            `json:"backup"`
	Type         recipe.Type       `json:"type"`
	Kind         command.PointKind `json:"kind"`
	Created      time.Time         `json:"created"`
	SHA256       string            `json:"sha256"`
	Size         int64             `json:"size"`     // stored (compressed) bytes
	RawSize      int64             `json:"raw_size"` // the database or tar stream before compression
	Fingerprint  string            `json:"fingerprint"`
	AgentVersion string            `json:"agent_version"`
}

// Point is one restore point as found in a backup's folder.
type Point struct {
	ID          string
	Time        time.Time
	Kind        command.PointKind
	DataKey     string // "" when the data object is missing
	ManifestKey string // "" when the manifest is missing
	Size        int64  // the data object's size
}

// Complete reports whether both the data object and its manifest exist.
func (p Point) Complete() bool { return p.DataKey != "" && p.ManifestKey != "" }

// Keys are the point's existing objects.
func (p Point) Keys() []string {
	var out []string
	for _, k := range []string{p.DataKey, p.ManifestKey} {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Info is the point as the API lists it, with when the ceiling deletes it.
func (p Point) Info(ceilingDays int) command.RestorePoint {
	info := command.RestorePoint{ID: p.ID, Time: p.Time.Unix(), Kind: p.Kind, Size: p.Size}
	if e := Expires(p, ceilingDays); !e.IsZero() {
		info.Expires = e.Unix()
	}
	return info
}

// Points pairs the objects listed under folder into points, newest first.
// Keys that are not a point's data object or manifest are ignored.
func Points(objects []store.Object, folder string) []Point {
	byID := map[string]*Point{}
	for _, o := range objects {
		name, ok := strings.CutPrefix(o.Key, folder)
		if !ok || strings.Contains(name, "/") {
			continue
		}
		id, isManifest := strings.CutSuffix(name, ManifestExt)
		if !isManifest {
			var found bool
			for _, ext := range []string{DataExt(recipe.TypeSQLite), DataExt(recipe.TypeFiles)} {
				if base, ok := strings.CutSuffix(name, ext); ok {
					id, found = base, true
				}
			}
			if !found {
				continue
			}
		}
		t, kind, ok := ParsePointID(id)
		if !ok {
			continue
		}
		p := byID[id]
		if p == nil {
			p = &Point{ID: id, Time: t, Kind: kind}
			byID[id] = p
		}
		if isManifest {
			p.ManifestKey = o.Key
		} else {
			p.DataKey, p.Size = o.Key, o.Size
		}
	}
	out := make([]Point, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Complete filters the complete points.
func Complete(points []Point) []Point {
	var out []Point
	for _, p := range points {
		if p.Complete() {
			out = append(out, p)
		}
	}
	return out
}

// Newest is the newest complete point of one of kinds (ok false: none).
func Newest(points []Point, kinds ...command.PointKind) (Point, bool) {
	for _, p := range points { // newest first
		if p.Complete() && contains(kinds, p.Kind) {
			return p, true
		}
	}
	return Point{}, false
}

func contains(kinds []command.PointKind, k command.PointKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Policy is a retention: the backup's tiers and the server's ceiling
// (BACKUP_RETENTION_DAYS, 0 = none).
type Policy struct {
	Keep        recipe.Retention
	CeilingDays int
}

// ceiling is the age past which every copy goes (0 = none).
func (p Policy) ceiling() time.Duration { return time.Duration(p.CeilingDays) * 24 * time.Hour }

// Prune returns the points retention deletes at now:
//   - any point older than the ceiling, whatever its kind (the ceiling
//     overrides every rule below);
//   - an incomplete point older than IncompleteAfter;
//   - a pre-restore point older than PreRestoreKeep;
//   - a scheduled point no tier keeps: every copy younger than Keep.Recent,
//     then the newest of each of the last Keep.Daily UTC days and the last
//     Keep.Monthly UTC months; the newest complete scheduled copy is always
//     kept (below the ceiling).
//
// Manual points go only by the ceiling (or by hand).
func Prune(points []Point, p Policy, now time.Time) []Point {
	now = now.UTC()
	ceiling := p.ceiling()
	keepDay, keepMonth := map[int]bool{}, map[int]bool{}
	newestScheduled := true
	var out []Point
	for _, pt := range points { // newest first
		age := now.Sub(pt.Time)
		switch {
		case ceiling > 0 && age > ceiling:
			out = append(out, pt)
		case !pt.Complete():
			if age > IncompleteAfter {
				out = append(out, pt)
			}
		case pt.Kind == command.PointPreRestore:
			if age > PreRestoreKeep {
				out = append(out, pt)
			}
		case pt.Kind == command.PointScheduled:
			day, month := daysBetween(pt.Time, now), monthsBetween(pt.Time, now)
			keep := newestScheduled || age < p.Keep.Recent
			if day < p.Keep.Daily && !keepDay[day] {
				keepDay[day], keep = true, true
			}
			if month < p.Keep.Monthly && !keepMonth[month] {
				keepMonth[month], keep = true, true
			}
			newestScheduled = false
			if !keep {
				out = append(out, pt)
			}
		}
	}
	return out
}

// daysBetween counts UTC calendar days from t to now (0 = the same day).
func daysBetween(t, now time.Time) int {
	y1, m1, d1 := t.UTC().Date()
	y2, m2, d2 := now.Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a) / (24 * time.Hour))
}

// monthsBetween counts UTC calendar months from t to now (0 = the same month).
func monthsBetween(t, now time.Time) int {
	y1, m1, _ := t.UTC().Date()
	y2, m2, _ := now.Date()
	return (y2-y1)*12 + int(m2-m1)
}

// NeedsRefresh reports whether a run must upload a copy even when nothing
// changed: the newest complete scheduled copy is older than half the
// ceiling, so an idle backup's only copy is replaced before the ceiling
// deletes it. Never with no ceiling, or with no scheduled copy yet (the run
// uploads anyway: there is nothing to compare with).
func NeedsRefresh(points []Point, ceilingDays int, now time.Time) bool {
	if ceilingDays <= 0 {
		return false
	}
	p, ok := Newest(points, command.PointScheduled)
	return ok && now.Sub(p.Time) > time.Duration(ceilingDays)*12*time.Hour
}

// Expires is when a point reaches the ceiling (zero time: never).
func Expires(p Point, ceilingDays int) time.Time {
	if ceilingDays <= 0 {
		return time.Time{}
	}
	return p.Time.Add(time.Duration(ceilingDays) * 24 * time.Hour)
}
