package command

import (
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/backup/recipe"
)

// Backups: the agent runs an app's enabled sqlite and files backups on
// schedule (or on request) and stores each copy — a restore point — in the
// storage the backup names, under <storage prefix><app-id>/<backup>/.

// BackupState is a backup's health.
type BackupState string

const (
	BackupOff     BackupState = "off"     // disabled
	BackupRunning BackupState = "running" // a run is in progress
	BackupNever   BackupState = "never"   // enabled, not run yet
	BackupOK      BackupState = "ok"      // the last run succeeded (within 2 × every when scheduled)
	BackupLate    BackupState = "late"    // scheduled, no success within 2 × every
	BackupFailing BackupState = "failing" // the last attempt failed
)

// BackupHealth is one backup of an app in apps.status. LastSuccess is unix
// seconds (0 = never).
type BackupHealth struct {
	Name        string      `json:"name"`
	State       BackupState `json:"state"`
	LastSuccess int64       `json:"last_success,omitempty"`
	LastError   string      `json:"last_error,omitempty"`
}

// PointKind is why a restore point was made.
type PointKind string

const (
	PointScheduled  PointKind = "scheduled"
	PointManual     PointKind = "manual"
	PointPreRestore PointKind = "pre-restore"
)

// RestorePoint is one stored copy of a backup. ID names it in
// delete-point (and restore); Time is unix seconds; Size is the stored
// (compressed) size in bytes; Expires is when BACKUP_RETENTION_DAYS deletes
// it (unix seconds, 0 = never).
type RestorePoint struct {
	ID      string    `json:"id"`
	Time    int64     `json:"time"`
	Kind    PointKind `json:"kind"`
	Size    int64     `json:"size"`
	Expires int64     `json:"expires,omitempty"`
}

// validateBackupRef checks the app id and backup name of a backup-scoped
// request.
func validateBackupRef(appID, backup string) error {
	if err := validateAppID(appID); err != nil {
		return err
	}
	return recipe.ValidateName(backup)
}

// BackupRunWait bounds how long the agent holds a Back up now request for its
// run (the runs queued ahead of it count too). The API awaits the result
// longer, so the agent's timeout answer still finds its owner.
const BackupRunWait = 2 * time.Hour

// --- backup.update ----------------------------------------------------------

// UpdateBackupsRequest replaces an app's backups list (its overrides) as a
// whole; the agent validates it against the compose file's recipe and the
// server's storages, commits it as a new revision, and redeploys only when
// the compose profiles or the storage variables change.
type UpdateBackupsRequest struct {
	AppID   string                `json:"app_id"`
	Backups []recipe.BackupConfig `json:"backups"`
}

func (r UpdateBackupsRequest) Validate() error {
	if err := validateAppID(r.AppID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, b := range r.Backups {
		if err := recipe.ValidateName(b.Name); err != nil {
			return fmt.Errorf("backups[%d].name: %w", i, err)
		}
		if seen[b.Name] {
			return fmt.Errorf("backups[%d].name: %q is listed twice", i, b.Name)
		}
		seen[b.Name] = true
		if b.Enabled && b.Storage == "" {
			return fmt.Errorf("backups[%d].storage: an enabled backup needs a storage", i)
		}
	}
	return nil
}

// UpdateBackupsResponse is the new revision and the effective backups.
// Redeployed tells whether the app was brought up again; Warnings are
// overrides dropped and redeploys skipped or failed.
type UpdateBackupsResponse struct {
	AppID      string                   `json:"app_id"`
	Revision   string                   `json:"revision"`
	Redeployed bool                     `json:"redeployed"`
	Backups    []recipe.EffectiveBackup `json:"backups"`
	Warnings   []string                 `json:"warnings,omitempty"`
}

// --- backup.points ----------------------------------------------------------

// ListRestorePointsRequest lists a backup's complete restore points.
type ListRestorePointsRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
}

func (r ListRestorePointsRequest) Validate() error { return validateBackupRef(r.AppID, r.Backup) }

// ListRestorePointsResponse lists the points newest first ([] for none).
type ListRestorePointsResponse struct {
	AppID  string         `json:"app_id"`
	Backup string         `json:"backup"`
	Points []RestorePoint `json:"points"`
}

// --- backup.run -------------------------------------------------------------

// RunBackupRequest runs a backup now (a manual copy). It fails while the
// same backup is already queued or running.
type RunBackupRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
}

func (r RunBackupRequest) Validate() error { return validateBackupRef(r.AppID, r.Backup) }

// RunBackupResponse is the outcome of the run. Unchanged means the data had
// not changed since the last copy, so nothing was uploaded; otherwise Point
// is the new restore point.
type RunBackupResponse struct {
	AppID     string        `json:"app_id"`
	Backup    string        `json:"backup"`
	Unchanged bool          `json:"unchanged"`
	Point     *RestorePoint `json:"point,omitempty"`
	Warnings  []string      `json:"warnings,omitempty"`
}

// --- backup.delete-point ----------------------------------------------------

// DeleteRestorePointRequest deletes one restore point (any kind) and its
// manifest.
type DeleteRestorePointRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

func (r DeleteRestorePointRequest) Validate() error {
	if err := validateBackupRef(r.AppID, r.Backup); err != nil {
		return err
	}
	if !ValidPointID(r.Point) {
		return errors.New("invalid restore point id")
	}
	return nil
}

type DeleteRestorePointResponse struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

// ValidPointID reports whether id names a restore point:
// <yyyymmddThhmmssZ>-<kind>.
func ValidPointID(id string) bool {
	if len(id) < 17 || id[8] != 'T' || id[15] != 'Z' || id[16] != '-' {
		return false
	}
	for i, c := range id[:15] {
		if i != 8 && (c < '0' || c > '9') {
			return false
		}
	}
	switch PointKind(id[17:]) {
	case PointScheduled, PointManual, PointPreRestore:
		return true
	}
	return false
}

// --- backup.restore ---------------------------------------------------------

// RestoreBackupRequest restores one restore point of a backup in place: the
// app is stopped, its current data saved as a pre-restore point, the point's
// data put back, and the app started again. The answer comes when the
// restore ends.
type RestoreBackupRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

func (r RestoreBackupRequest) Validate() error {
	if err := validateBackupRef(r.AppID, r.Backup); err != nil {
		return err
	}
	if !ValidPointID(r.Point) {
		return errors.New("invalid restore point id")
	}
	return nil
}

// RestoreBackupResponse is a finished restore: RestoredPoint is the point put
// back; PreRestorePoint the copy of the data it replaced ("" when there was
// none to save: no database file, or none of the paths). Warnings are paths
// the point did not hold (left as they were) and files skipped.
type RestoreBackupResponse struct {
	AppID           string   `json:"app_id"`
	Backup          string   `json:"backup"`
	RestoredPoint   string   `json:"restored_point"`
	PreRestorePoint string   `json:"pre_restore_point,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}
