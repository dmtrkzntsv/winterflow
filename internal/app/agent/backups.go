package agent

import (
	"context"
	"errors"
	"time"

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

// Backups is the backup scheduler as the dispatcher and the status reporter
// use it (*backup.Scheduler).
type Backups interface {
	// Reload rereads the backups after a change to an app or a storage.
	Reload()
	// Forget drops a deleted app's bookkeeping.
	Forget(appID string)
	// RunNow queues a manual run (it fails at once while the same backup is
	// queued or running); its outcome arrives when the
	// run ends.
	RunNow(appID, name string) <-chan backup.Outcome
	// Restore queues a restore of a point; its outcome arrives when the
	// restore ends.
	Restore(appID, name, point string) <-chan backup.Outcome
	// Decorate adds each app's backup health to its status.
	Decorate(apps []command.AppStatus, now time.Time) []command.AppStatus
}

var _ Backups = (*backup.Scheduler)(nil)

// errNoBackups answers backup.run and backup.restore on an agent without a scheduler (tests).
var errNoBackups = errors.New("backups are not available on this agent")

// noBackups is the Backups of a dispatcher built without a scheduler.
type noBackups struct{}

func (noBackups) Reload()       {}
func (noBackups) Forget(string) {}
func (noBackups) RunNow(string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
	ch <- backup.Outcome{Err: errNoBackups}
	return ch
}
func (noBackups) Restore(string, string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
	ch <- backup.Outcome{Err: errNoBackups}
	return ch
}
func (noBackups) Decorate(apps []command.AppStatus, _ time.Time) []command.AppStatus { return apps }

// SetBackups wires the backup scheduler (standalone and agent do; tests
// may not).
func (d *Dispatcher) SetBackups(b Backups) { d.backups = b }

// backupRunWait bounds how long a Back up now request waits for its run;
// the API waits command.BackupRunWait plus slack, so this answer arrives
// before the API gives up.
const backupRunWait = command.BackupRunWait

// runBackup waits for a Back up now request's run (on the backup scheduler,
// never on the command pipeline: backup.run is a detached command).
func (d *Dispatcher) runBackup(ctx context.Context, in command.RunBackupRequest) (command.RunBackupResponse, error) {
	// A detached command still has a limit: a scheduler that never answers
	// (not started, wedged) must not hold the request forever.
	ctx, cancel := context.WithTimeout(ctx, d.runWait)
	defer cancel()
	select {
	case o := <-d.backups.RunNow(in.AppID, in.Backup):
		return o.Result, o.Err
	case <-ctx.Done():
		return command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup}, ctx.Err()
	}
}

// restoreBackup waits for a restore request (on the backup scheduler, like
// backup.run: backup.restore is a detached command), bounded like it.
func (d *Dispatcher) restoreBackup(ctx context.Context, in command.RestoreBackupRequest) (command.RestoreBackupResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, d.runWait)
	defer cancel()
	select {
	case o := <-d.backups.Restore(in.AppID, in.Backup, in.Point):
		return o.Restore, o.Err
	case <-ctx.Done():
		return command.RestoreBackupResponse{AppID: in.AppID, Backup: in.Backup, RestoredPoint: in.Point}, ctx.Err()
	}
}

// HelperCleaner removes leftover backup helper containers.
type HelperCleaner interface {
	RemoveHelpers(ctx context.Context) error
}

// RunBackups is the backup goroutine: it removes the helpers a previous run
// of the agent left behind, then runs the scheduler until ctx is done.
func RunBackups(ctx context.Context, cleaner HelperCleaner, s *backup.Scheduler, log *logger.Logger) {
	if err := cleaner.RemoveHelpers(ctx); err != nil {
		log.Warn("remove leftover backup helpers", "error", err)
	}
	s.Run(ctx)
}

// WithBackupHealth is src with each app's backup health added: what the
// status reporter sends, so a change in it reaches browsers like any other
// status change.
func WithBackupHealth(src StatusSource, b Backups) StatusSource {
	return healthSource{src: src, backups: b}
}

type healthSource struct {
	src     StatusSource
	backups Backups
}

func (h healthSource) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	apps, err := h.src.GetAppsStatus(ctx)
	if err != nil {
		return nil, err
	}
	return h.backups.Decorate(apps, time.Now()), nil
}
