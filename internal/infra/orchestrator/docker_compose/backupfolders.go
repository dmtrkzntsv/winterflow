package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// An app's backups live in <storage>/<app-id>/. When the app is deleted
// without its backups, the folder gets a .deleted marker: the daily sweep
// then applies BACKUP_RETENTION_DAYS to it and removes it once empty.
// Folders without a marker are never deleted automatically — they may be
// another server's.

// DeletedMarkerName is the marker's name in an app's folder.
const DeletedMarkerName = ".deleted"

// DeletedMarker is the marker's content.
type DeletedMarker struct {
	DeletedAt time.Time `json:"deleted_at"`
	AppName   string    `json:"app_name"`
}

// Time limits of the folder operations, each run in a goroutine abandoned at
// its limit (see withDeadline): delete-app and delete-orphan run on the
// command pipeline, the sweep on the backup goroutine.
const (
	folderTimeout      = 2 * time.Minute // delete-app's markers or purges, all storages; delete-orphan
	orphanListTimeout  = time.Minute     // listing a storage for its orphans
	sweepStorageBudget = 5 * time.Minute // the daily sweep of one storage
	deleteBatch        = 1000            // keys per Delete call
)

// appBackupStorages names the storages an app's backups use (any backup
// with a storage, on or off, custom included: a custom tool writes under
// the same folder through WF_BACKUP_*_PREFIX). File reads only.
func (r *Repository) appBackupStorages(appID string) []string {
	dir := r.appDataDir(appID)
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		r.log.Warn("delete: the app's backups can't be read, its backup folders are left as they are", "app_id", appID, "error", err)
		return nil
	}
	var out []string
	for _, b := range cfg.backups {
		if b.Storage != "" && !slices.Contains(out, b.Storage) {
			out = append(out, b.Storage)
		}
	}
	sort.Strings(out)
	return out
}

// settleBackups deletes a deleted app's backup folder in each storage
// (purge), or writes its .deleted marker there. Failures are warnings.
func (r *Repository) settleBackups(ctx context.Context, appID, appName string, storages []string, purge bool) []string {
	if len(storages) == 0 {
		return nil
	}
	what := "mark the backups of the deleted app"
	if purge {
		what = "delete the app's backups"
	}
	warnings, err := withDeadlineValue(ctx, folderTimeout, what, func(ctx context.Context) ([]string, error) {
		var out []string
		for _, name := range storages {
			st, err := r.openBackupStorage(name)
			if err == nil {
				if purge {
					_, err = deleteFolder(ctx, st, appID+"/")
				} else {
					err = writeMarker(ctx, st, appID, DeletedMarker{DeletedAt: r.now().UTC(), AppName: appName})
				}
			}
			if err != nil {
				out = append(out, fmt.Sprintf("backups in %s: %s: %v", name, what, err))
			}
		}
		return out, nil
	})
	if err != nil {
		return []string{err.Error()}
	}
	return warnings
}

func writeMarker(ctx context.Context, st store.Store, appID string, m DeletedMarker) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return st.Put(ctx, appID+"/"+DeletedMarkerName, strings.NewReader(string(raw)+"\n"))
}

func readMarker(ctx context.Context, st store.Store, key string) (DeletedMarker, error) {
	rc, err := st.Get(ctx, key)
	if err != nil {
		return DeletedMarker{}, err
	}
	defer rc.Close()
	var m DeletedMarker
	err = json.NewDecoder(io.LimitReader(rc, 64<<10)).Decode(&m)
	return m, err
}

// deleteFolder deletes every object under prefix, in batches; it returns
// how many there were.
func deleteFolder(ctx context.Context, st store.Store, prefix string) (int, error) {
	objs, err := st.List(ctx, prefix)
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		keys = append(keys, o.Key)
	}
	return len(keys), deleteKeys(ctx, st, keys)
}

func deleteKeys(ctx context.Context, st store.Store, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), deleteBatch)
		if err := st.Delete(ctx, keys[:n]...); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

// appFolder is the objects of one app's folder in a storage.
type appFolder struct {
	appID   string
	marker  string // the .deleted marker's key ("" = none)
	objects []store.Object
	points  []layout.Point // across its backups, every kind, complete or not
}

// appFolders groups a storage's objects by app folder (keys outside one —
// a top-level object — are not an app's).
func appFolders(objs []store.Object) []*appFolder {
	byApp := map[string]*appFolder{}
	var order []string
	byBackup := map[string][]store.Object{}
	for _, o := range objs {
		app, rest, ok := strings.Cut(o.Key, "/")
		if !ok || app == "" {
			continue
		}
		f := byApp[app]
		if f == nil {
			f = &appFolder{appID: app}
			byApp[app] = f
			order = append(order, app)
		}
		if rest == DeletedMarkerName {
			f.marker = o.Key
			continue
		}
		f.objects = append(f.objects, o)
		if backup, _, ok := strings.Cut(rest, "/"); ok {
			byBackup[app+"/"+backup+"/"] = append(byBackup[app+"/"+backup+"/"], o)
		}
	}
	for folder, list := range byBackup {
		app, _, _ := strings.Cut(folder, "/")
		byApp[app].points = append(byApp[app].points, layout.Points(list, folder)...)
	}
	sort.Strings(order)
	out := make([]*appFolder, 0, len(order))
	for _, app := range order {
		out = append(out, byApp[app])
	}
	return out
}

// newest is the time of the folder's newest object: a point's own time,
// any other object's mtime (zero when it has none but its marker).
func (f *appFolder) newest() time.Time {
	var t time.Time
	for _, o := range f.objects {
		ot := o.ModTime
		if p, ok := f.pointOf(o.Key); ok {
			ot = p.Time
		}
		if ot.After(t) {
			t = ot
		}
	}
	return t
}

// pointOf finds the point a key belongs to.
func (f *appFolder) pointOf(key string) (layout.Point, bool) {
	for _, p := range f.points {
		if p.DataKey == key || p.ManifestKey == key {
			return p, true
		}
	}
	return layout.Point{}, false
}

// SweepDeleted applies BACKUP_RETENTION_DAYS to the folders of deleted apps
// (those with a .deleted marker) in every storage: what is older than the
// ceiling is deleted — a point by its own time, any other object (a custom
// tool's) by its mtime — and a folder left with nothing but its marker is
// removed with it. The backup goroutine runs it once a day; 0 disables it.
// It returns what failed, per storage.
func (r *Repository) SweepDeleted(ctx context.Context) []string {
	days := r.cfg.GetBackupRetentionDays()
	if days <= 0 {
		return nil
	}
	list, err := r.storages.List()
	if err != nil {
		return []string{"sweep: " + err.Error()}
	}
	var problems []string
	for _, s := range list {
		err := withDeadline(ctx, sweepStorageBudget, "sweep "+s.Name, func(ctx context.Context) error {
			st, err := r.openBackupStorage(s.Name)
			if err != nil {
				return err
			}
			return r.sweepStorage(ctx, st, days)
		})
		if err != nil {
			problems = append(problems, s.Name+": "+err.Error())
		}
	}
	return problems
}

func (r *Repository) sweepStorage(ctx context.Context, st store.Store, days int) error {
	objs, err := st.List(ctx, "")
	if err != nil {
		return err
	}
	cutoff := r.now().Add(-time.Duration(days) * 24 * time.Hour)
	for _, f := range appFolders(objs) {
		if f.marker == "" || r.appExists(f.appID) {
			continue
		}
		var old []string
		left := 0
		for _, o := range f.objects {
			t := o.ModTime
			if p, ok := f.pointOf(o.Key); ok {
				t = p.Time
			}
			if t.Before(cutoff) {
				old = append(old, o.Key)
			} else {
				left++
			}
		}
		if left == 0 {
			old = append(old, f.marker) // last: a failure keeps the folder marked
		}
		if err := deleteKeys(ctx, st, old); err != nil {
			return fmt.Errorf("app %s: %w", f.appID, err)
		}
	}
	return nil
}

// ListOrphans lists the backup folders in a storage whose app is not on this
// server. A folder counts when it holds a restore point or a .deleted marker
// (an app's own files written through WF_STORAGE_ variables don't make one).
// On request only, within orphanListTimeout.
func (r *Repository) ListOrphans(ctx context.Context, name string) ([]command.Orphan, error) {
	return withDeadlineValue(ctx, orphanListTimeout, "list the backups of other apps", func(ctx context.Context) ([]command.Orphan, error) {
		st, err := r.openBackupStorage(name)
		if err != nil {
			return nil, err
		}
		objs, err := st.List(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", name, err)
		}
		days := r.cfg.GetBackupRetentionDays()
		out := []command.Orphan{}
		for _, f := range appFolders(objs) {
			if !command.ValidAppID(f.appID) || r.appExists(f.appID) || (f.marker == "" && len(f.points) == 0) {
				continue
			}
			o := command.Orphan{AppID: f.appID}
			for _, obj := range f.objects {
				o.Bytes += obj.Size
			}
			for _, p := range layout.Complete(f.points) {
				o.Copies++
				o.Newest = max(o.Newest, p.Time.Unix())
			}
			if f.marker != "" {
				if m, err := readMarker(ctx, st, f.marker); err == nil {
					o.AppName, o.DeletedAt = m.AppName, m.DeletedAt.Unix()
				}
				if days > 0 {
					purge := f.newest()
					if purge.IsZero() {
						purge = time.Unix(o.DeletedAt, 0) // nothing left: the next sweep
					} else {
						purge = purge.Add(time.Duration(days) * 24 * time.Hour)
					}
					o.PurgeAt = purge.Unix()
				}
			}
			out = append(out, o)
		}
		return out, nil
	})
}

// DeleteOrphan deletes an orphan's folder in a storage: every object under
// <app-id>/. It refuses for an app on this server (delete the app, or its
// restore points, instead).
func (r *Repository) DeleteOrphan(ctx context.Context, name, appID string) (int, error) {
	if r.appExists(appID) {
		return 0, fmt.Errorf("the app %s is on this server: its backups are not orphans", r.appName(r.appDataDir(appID), appID))
	}
	return withDeadlineValue(ctx, folderTimeout, "delete the backups", func(ctx context.Context) (int, error) {
		st, err := r.openBackupStorage(name)
		if err != nil {
			return 0, err
		}
		return deleteFolder(ctx, st, appID+"/")
	})
}
