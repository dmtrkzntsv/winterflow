package agent

import (
	"context"
	"errors"
	"time"

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
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

func (noBackups) Reload()                                     {}
func (noBackups) Forget(string)                               {}
func (noBackups) RunNow(string, string) <-chan backup.Outcome { return unavailable() }
func (noBackups) Restore(string, string, string) <-chan backup.Outcome {
	return unavailable()
}

func unavailable() <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
	ch <- backup.Outcome{Err: errNoBackups}
	return ch
}
func (noBackups) Decorate(apps []command.AppStatus, _ time.Time) []command.AppStatus { return apps }

// SetBackups wires the backup scheduler (standalone and agent do; tests
// may not).
func (d *Dispatcher) SetBackups(b Backups) { d.backups = b }

// awaitOutcome waits for a detached backup command's outcome (on the backup
// scheduler, never on the command pipeline), at most the dispatcher's run
// wait: a scheduler that never answers (not started, wedged) must not hold
// the request forever. The API waits command.BackupRunWait plus slack, so the
// timeout answer arrives before the API gives up. pick takes the response
// out of the outcome; fallback is the response of a timeout.
func awaitOutcome[T any](ctx context.Context, wait time.Duration, ch <-chan backup.Outcome, pick func(backup.Outcome) T, fallback T) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case o := <-ch:
		return pick(o), o.Err
	case <-ctx.Done():
		return fallback, ctx.Err()
	}
}

// runBackup waits for a Back up now request's run (backup.run is a detached
// command).
func (d *Dispatcher) runBackup(ctx context.Context, in command.RunBackupRequest) (command.RunBackupResponse, error) {
	return awaitOutcome(ctx, d.runWait, d.backups.RunNow(in.AppID, in.Backup),
		func(o backup.Outcome) command.RunBackupResponse { return o.Result },
		command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup})
}

// restoreBackup waits for a restore request (backup.restore is a detached
// command), bounded like runBackup.
func (d *Dispatcher) restoreBackup(ctx context.Context, in command.RestoreBackupRequest) (command.RestoreBackupResponse, error) {
	return awaitOutcome(ctx, d.runWait, d.backups.Restore(in.AppID, in.Backup, in.Point),
		func(o backup.Outcome) command.RestoreBackupResponse { return o.Restore },
		command.RestoreBackupResponse{AppID: in.AppID, Backup: in.Backup, RestoredPoint: in.Point})
}

// HelperCleaner removes leftover backup helper containers, and reports the
// restores a previous run of the agent was cut short in.
type HelperCleaner interface {
	RemoveHelpers(ctx context.Context) error
	// InterruptedRestores reads (once) what the last run left in
	// backups/restoring.json.
	InterruptedRestores() []dockercompose.RestoreMark
}

// RunBackups is the backup goroutine: it notes the restores a previous run
// of the agent was cut short in (their backups' last_error tells the user to
// check the app's data), removes the helpers it left behind, then runs the
// scheduler until ctx is done.
func RunBackups(ctx context.Context, cleaner HelperCleaner, s *backup.Scheduler, log *logger.Logger) {
	s.NoteInterrupted(cleaner.InterruptedRestores())
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

// StatusReport is the wrapped source's report with the health added.
func (h healthSource) StatusReport(ctx context.Context) (command.GetAppsStatusResponse, error) {
	resp, err := h.src.StatusReport(ctx)
	if err != nil {
		return command.GetAppsStatusResponse{}, err
	}
	resp.Apps = h.backups.Decorate(resp.Apps, time.Now())
	return resp, nil
}
