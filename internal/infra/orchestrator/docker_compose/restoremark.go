package dockercompose

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"winterflow/pkg/fsutil"
)

// restoreMarkName is the file, in {AGENT_DATA_DIR}/backups, that records the
// restores in progress. A restore that the agent's stop (an update, a
// restart, a host shutdown, a crash) cuts short leaves the app stopped and
// its data perhaps half replaced; nothing else would tell the user. The file
// is there from just before the app is stopped until the restore has ended,
// and the next start of the agent reads it (see InterruptedRestores).
const restoreMarkName = "restoring.json"

// Phases of a restore, as the marker records them.
const (
	restorePhaseStopping = "stopping"         // the app is being stopped
	restorePhaseCopy     = "pre-restore-copy" // the current data is being saved
	restorePhaseHelper   = "restoring"        // the helper may be replacing the data
)

// RestoreMark is one restore in progress, as stored in restoring.json.
type RestoreMark struct {
	App    string `json:"app"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
	// PreRestorePoint is the copy of the data saved before the restore; empty
	// until it exists (or when there was nothing to save).
	PreRestorePoint string    `json:"pre_restore_point,omitempty"`
	WasRunning      bool      `json:"was_running"`
	Phase           string    `json:"phase"`
	Started         time.Time `json:"started"`
}

// Message is what the user is told about a restore that was interrupted: the
// backup's last_error.
func (m RestoreMark) Message() string {
	copyNote := "no pre-restore copy had been saved yet"
	if m.PreRestorePoint != "" {
		copyNote = "pre-restore copy " + m.PreRestorePoint
	}
	msg := fmt.Sprintf("the agent stopped during a restore of %s (%s); check the app's data", m.Point, copyNote)
	if m.Phase == restorePhaseHelper {
		msg += ": it may be partly replaced"
	}
	if m.WasRunning {
		msg += ", and the app may have been left stopped"
	}
	return msg
}

func (r *Repository) restoreMarkPath() string {
	return filepath.Join(r.cfg.GetAgentDataDir(), "backups", restoreMarkName)
}

// writeRestoreMarksLocked stores the marks (0600, atomically), or removes
// the file when there are none. The caller holds markMu.
func (r *Repository) writeRestoreMarksLocked() error {
	path := r.restoreMarkPath()
	if len(r.marks) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	list := make([]RestoreMark, 0, len(r.marks))
	for _, m := range r.marks {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].App < list[j].App })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFile(path, raw, 0o600)
}

// setRestoreMark records (or updates) the restore of m.App.
func (r *Repository) setRestoreMark(m RestoreMark) error {
	r.markMu.Lock()
	defer r.markMu.Unlock()
	if r.marks == nil {
		r.marks = map[string]RestoreMark{}
	}
	r.marks[m.App] = m
	return r.writeRestoreMarksLocked()
}

// updateRestoreMark changes the restore of app's marker, if there is one.
func (r *Repository) updateRestoreMark(app string, change func(*RestoreMark)) {
	r.markMu.Lock()
	defer r.markMu.Unlock()
	m, ok := r.marks[app]
	if !ok {
		return
	}
	change(&m)
	r.marks[app] = m
	if err := r.writeRestoreMarksLocked(); err != nil {
		r.log.Warn("record the restore in progress", "app_id", app, "error", err)
	}
}

// clearRestoreMark removes the marker of app's restore: it has ended, however
// it ended (its error says what state the app is left in).
func (r *Repository) clearRestoreMark(app string) {
	r.markMu.Lock()
	defer r.markMu.Unlock()
	if _, ok := r.marks[app]; !ok {
		return
	}
	delete(r.marks, app)
	if err := r.writeRestoreMarksLocked(); err != nil {
		r.log.Warn("clear the restore in progress", "app_id", app, "error", err)
	}
}

// InterruptedRestores is what the agent's last run left in restoring.json:
// the restores a stop cut short. It removes the file, and names only the apps
// this agent still has. Nothing is started or rolled back: the data may be
// partly replaced, and what to do is the user's call (the message names the
// pre-restore copy). Call it once, when the agent starts, before any restore
// can begin.
func (r *Repository) InterruptedRestores() []RestoreMark {
	path := r.restoreMarkPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn("read the restores in progress", "error", err)
		}
		return nil
	}
	if err := os.Remove(path); err != nil {
		r.log.Warn("remove the restores in progress", "error", err)
	}
	var list []RestoreMark
	if err := json.Unmarshal(raw, &list); err != nil {
		r.log.Warn("the restores in progress are unreadable, dropping them", "error", err)
		return nil
	}
	var out []RestoreMark
	for _, m := range list {
		if r.appExists(m.App) {
			out = append(out, m)
		}
	}
	return out
}
