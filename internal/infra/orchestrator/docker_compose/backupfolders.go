package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
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
// command pipeline, the sweep on the backup goroutine. A folder is always
// marked before its data goes and unmarked last, so a limit hit half-way
// leaves a marked folder the daily sweep finishes.
const (
	folderTimeout      = 2 * time.Minute  // delete-app's markers or purges, all storages; delete-orphan
	storageMinBudget   = 30 * time.Second // the least one storage of delete-app is given
	orphanListTimeout  = time.Minute      // listing a storage for its orphans
	sweepStorageBudget = 5 * time.Minute  // the daily sweep of one storage
	deleteBatch        = 100              // keys per Delete call
	deleteWorkers      = 4                // Delete calls in flight (S3 deletes key by key)
	// markerSkew is how far a restore point may be newer than its folder's
	// marker before the marker is taken as stale (the app is live again):
	// room for clocks and an upload that lands just after the delete.
	markerSkew = time.Hour
)

// appBackupStorages names the storages an app's backups use (any backup
// with a storage, on or off, custom included: a custom tool writes under
// the same folder through WF_BACKUP_*_PREFIX). File reads only.
func (r *Repository) appBackupStorages(appID string) ([]string, error) {
	dir := r.appDataDir(appID)
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range cfg.backups {
		if b.Storage != "" && !slices.Contains(out, b.Storage) {
			out = append(out, b.Storage)
		}
	}
	sort.Strings(out)
	return out, nil
}

// settleBackups marks a deleted app's backup folder in each storage with its
// .deleted marker and, with purge, deletes what is in it and then the marker.
// The marker goes first and last, so a failure or a timeout leaves the folder
// marked for the daily sweep. Failures are warnings; purged reports whether
// every folder is gone.
func (r *Repository) settleBackups(ctx context.Context, appID, appName string, storages []string, purge bool) (warnings []string, purged bool) {
	purged = purge
	budget := max(folderTimeout/time.Duration(max(len(storages), 1)), storageMinBudget)
	what := "mark the backups of the deleted app"
	if purge {
		what = "delete the app's backups"
	}
	type outcome struct {
		marked bool
		err    error
	}
	for _, name := range storages {
		out, err := withDeadlineValue(ctx, budget, what, func(ctx context.Context) (outcome, error) {
			st, err := r.openBackupStorage(name)
			if err != nil {
				return outcome{}, err
			}
			m := DeletedMarker{DeletedAt: r.now().UTC(), AppName: appName}
			if !purge {
				return outcome{}, writeMarker(ctx, st, appID, m)
			}
			marked, err := purgeFolder(ctx, st, appID, &m)
			return outcome{marked: marked, err: err}, err
		})
		if err == nil {
			continue
		}
		purged = false
		msg := fmt.Sprintf("backups in %s: %s: %v", name, what, err)
		switch {
		case !purge:
		case out.marked:
			msg += "; the folder stays marked as deleted: the daily sweep removes what is left past BACKUP_RETENTION_DAYS"
		default:
			msg += "; the folder may still hold backups: delete it from the backups of other apps"
		}
		warnings = append(warnings, msg)
	}
	return warnings, purged
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

// purgeFolder deletes an app's folder: the .deleted marker is written first
// (set mark; with nil the folder's own marker, if any, stays as it is), the
// other objects go next and the marker last. marked reports whether the
// folder carries a marker once the data deletion starts.
func purgeFolder(ctx context.Context, st store.Store, appID string, mark *DeletedMarker) (marked bool, err error) {
	prefix := appID + "/"
	objs, err := st.List(ctx, prefix)
	if err != nil {
		return false, err
	}
	return purgeObjects(ctx, st, appID, objs, mark)
}

func purgeObjects(ctx context.Context, st store.Store, appID string, objs []store.Object, mark *DeletedMarker) (marked bool, err error) {
	markerKey := appID + "/" + DeletedMarkerName
	hasMarker := false
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		if o.Key == markerKey {
			hasMarker = true
			continue
		}
		keys = append(keys, o.Key)
	}
	if mark != nil {
		if err := writeMarker(ctx, st, appID, *mark); err != nil {
			return false, fmt.Errorf("write the marker: %w", err)
		}
		hasMarker = true
	}
	if err := deleteKeys(ctx, st, keys); err != nil {
		return hasMarker, err
	}
	if hasMarker {
		if err := st.Delete(ctx, markerKey); err != nil {
			return true, err
		}
	}
	return hasMarker, nil
}

// deleteKeys deletes keys in batches, a few at a time: S3 deletes key by key,
// so a long folder would otherwise take minutes. The first error stops it.
func deleteKeys(ctx context.Context, st store.Store, keys []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, deleteWorkers)
	for len(keys) > 0 && ctx.Err() == nil {
		n := min(len(keys), deleteBatch)
		batch := keys[:n]
		keys = keys[n:]
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			if err := st.Delete(ctx, batch...); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

// appFolder is the objects of one app's folder in a storage.
type appFolder struct {
	appID      string
	marker     string    // the .deleted marker's key ("" = none)
	markerMod  time.Time // and its mtime
	objects    []store.Object
	points     []layout.Point       // across its backups, every kind, complete or not
	pointTimes map[string]time.Time // a point's data and manifest keys -> its time
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
			f = &appFolder{appID: app, pointTimes: map[string]time.Time{}}
			byApp[app] = f
			order = append(order, app)
		}
		if rest == DeletedMarkerName {
			f.marker, f.markerMod = o.Key, o.ModTime
			continue
		}
		f.objects = append(f.objects, o)
		if backup, _, ok := strings.Cut(rest, "/"); ok {
			byBackup[app+"/"+backup+"/"] = append(byBackup[app+"/"+backup+"/"], o)
		}
	}
	for folder, list := range byBackup {
		app, _, _ := strings.Cut(folder, "/")
		f := byApp[app]
		for _, p := range layout.Points(list, folder) {
			f.points = append(f.points, p)
			f.pointTimes[p.DataKey], f.pointTimes[p.ManifestKey] = p.Time, p.Time
		}
	}
	sort.Strings(order)
	out := make([]*appFolder, 0, len(order))
	for _, app := range order {
		out = append(out, byApp[app])
	}
	return out
}

// timeOf is an object's age reference: a point's own time, any other
// object's mtime.
func (f *appFolder) timeOf(o store.Object) time.Time {
	if t, ok := f.pointTimes[o.Key]; ok {
		return t
	}
	return o.ModTime
}

// newest is the time of the folder's newest object (zero when it has none
// but its marker).
func (f *appFolder) newest() time.Time {
	var t time.Time
	for _, o := range f.objects {
		if ot := f.timeOf(o); ot.After(t) {
			t = ot
		}
	}
	return t
}

// readDeleted reads the folder's marker. A marker that can't be read still
// counts, dated by its mtime. A marker is stale when the app lives again:
// the folder holds a restore point newer than the marker (see markerSkew) —
// then stale is true and the folder is not a deleted app's.
func (f *appFolder) readDeleted(ctx context.Context, st store.Store) (m DeletedMarker, stale bool) {
	m, err := readMarker(ctx, st, f.marker)
	if err != nil || m.DeletedAt.IsZero() {
		m.DeletedAt = f.markerMod
	}
	for _, p := range layout.Complete(f.points) {
		if p.Time.After(m.DeletedAt.Add(markerSkew)) {
			return m, true
		}
	}
	return m, false
}

// SweepDeleted applies BACKUP_RETENTION_DAYS to the folders of deleted apps
// (those with a .deleted marker) in every storage: what is older than the
// ceiling is deleted — a point by its own time, any other object (a custom
// tool's) by its mtime — and a folder left with nothing but its marker is
// removed with it. A folder whose newest restore point is newer than its
// marker is a live app's (stale marker): it is skipped and reported. The
// backup goroutine runs it once a day; 0 disables it. It returns what
// failed, per storage.
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

// sweepStorage sweeps every marked folder; one that fails doesn't stop the
// others, and its error is returned with theirs.
func (r *Repository) sweepStorage(ctx context.Context, st store.Store, days int) error {
	objs, err := st.List(ctx, "")
	if err != nil {
		return err
	}
	cutoff := r.now().Add(-time.Duration(days) * 24 * time.Hour)
	var errs []error
	for _, f := range appFolders(objs) {
		if f.marker == "" || r.appExists(f.appID) {
			continue
		}
		if _, stale := f.readDeleted(ctx, st); stale {
			errs = append(errs, fmt.Errorf("app %s: a restore point is newer than the folder's deleted marker (the app is live elsewhere?), so the folder is not swept", f.appID))
			continue
		}
		var old []string
		left := 0
		for _, o := range f.objects {
			if f.timeOf(o).Before(cutoff) {
				old = append(old, o.Key)
			} else {
				left++
			}
		}
		if err := deleteKeys(ctx, st, old); err != nil {
			errs = append(errs, fmt.Errorf("app %s: %w", f.appID, err))
			continue
		}
		if left == 0 { // last: a failure keeps the folder marked
			if err := st.Delete(ctx, f.marker); err != nil {
				errs = append(errs, fmt.Errorf("app %s: %w", f.appID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// ListOrphans lists the backup folders in a storage whose app is not on this
// server. A folder counts when it holds a restore point or a .deleted marker
// (an app's own files written through WF_STORAGE_ variables don't make one).
// A stale marker (see readDeleted) is ignored. On request only, within
// orphanListTimeout.
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
				if m, stale := f.readDeleted(ctx, st); !stale {
					o.AppName, o.DeletedAt = m.AppName, m.DeletedAt.Unix()
					if days > 0 {
						purge := f.newest()
						if purge.IsZero() {
							purge = m.DeletedAt // nothing left: the next sweep
						} else {
							purge = purge.Add(time.Duration(days) * 24 * time.Hour)
						}
						o.PurgeAt = purge.Unix()
					}
				}
			}
			out = append(out, o)
		}
		return out, nil
	})
}

// DeleteOrphan deletes an orphan's folder in a storage: every object under
// <app-id>/, the .deleted marker written first (when it has none) and removed
// last, so a failure leaves the folder marked for the daily sweep. It refuses
// an app on this server (delete the app, or its restore points, instead) and
// a folder that holds no backups (no restore point, no marker): that may be
// an app's own files.
func (r *Repository) DeleteOrphan(ctx context.Context, name, appID string) (int, error) {
	if !command.ValidAppID(appID) {
		return 0, fmt.Errorf("%q is not an app id", appID)
	}
	if r.appExists(appID) {
		return 0, fmt.Errorf("the app %s is on this server: its backups are not orphans", r.appName(r.appDataDir(appID), appID))
	}
	return withDeadlineValue(ctx, folderTimeout, "delete the backups", func(ctx context.Context) (int, error) {
		st, err := r.openBackupStorage(name)
		if err != nil {
			return 0, err
		}
		objs, err := st.List(ctx, appID+"/")
		if err != nil {
			return 0, err
		}
		if len(objs) == 0 {
			return 0, nil
		}
		var mark *DeletedMarker
		for _, f := range appFolders(objs) {
			if f.marker == "" && len(f.points) == 0 {
				return 0, fmt.Errorf("the folder %s in %s holds no backups (no restore point, no deleted marker), so it is not deleted", appID, name)
			}
			if f.marker == "" {
				mark = &DeletedMarker{DeletedAt: r.now().UTC()}
			}
		}
		if _, err := purgeObjects(ctx, st, appID, objs, mark); err != nil {
			return 0, err
		}
		return len(objs), nil
	})
}
