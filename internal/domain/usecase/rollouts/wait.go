package rollouts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
)

// awaitResult waits for a dispatched command's result, at most uc.cmdWait;
// a failed command is its error.
func (uc *UseCase) awaitResult(ctx context.Context, ch <-chan port.CommandResult) (port.CommandResult, error) {
	t := time.NewTimer(uc.cmdWait)
	defer t.Stop()
	select {
	case res := <-ch:
		if !res.Success {
			if res.Error == "" {
				res.Error = "the agent reported a failure"
			}
			return res, errors.New(res.Error)
		}
		return res, nil
	case <-t.C:
		return port.CommandResult{}, fmt.Errorf("no answer from the server within %s", uc.cmdWait)
	case <-ctx.Done():
		return port.CommandResult{}, ctx.Err()
	}
}

// awaitRunning waits, at most uc.health, until a status report received
// after since shows the app running (Active). At the bound an app whose
// other containers exited cleanly (Idle: a finished one-shot container,
// which never reads Active) passes; no report, no containers, or
// Restarting, Problematic or Stopped fails with the reason. A Docker
// healthcheck's "unhealthy" is not seen: such a container reads running.
// It returns the last status seen for the app.
func (uc *UseCase) awaitRunning(ctx context.Context, serverID, appID string, since time.Time) (command.AppStatus, error) {
	deadline := uc.now().Add(uc.health)
	var (
		last             command.AppStatus
		reported, listed bool
	)
	for {
		if rep, ok := uc.d.Status.LatestReport(serverID, uc.now()); ok && rep.Received.After(since) {
			reported = true
			last, listed = findApp(rep.Apps, appID)
			if listed && last.StatusCode == command.ContainerStatusActive {
				return last, nil
			}
		}
		if !uc.now().Before(deadline) {
			break
		}
		if !sleep(ctx, uc.healthPoll) {
			return last, ctx.Err()
		}
	}
	switch {
	case !reported:
		return last, fmt.Errorf("the server sent no status report within %s", uc.health)
	case !listed:
		return last, fmt.Errorf("the app has no containers %s later", uc.health)
	case last.StatusCode == command.ContainerStatusIdle:
		return last, nil
	}
	return last, fmt.Errorf("the app is not running %s later (%s)", uc.health, statusWord(last.StatusCode))
}

// findApp is appID's status in a report.
func findApp(apps []command.AppStatus, appID string) (command.AppStatus, bool) {
	for _, a := range apps {
		if a.AppID == appID {
			return a, true
		}
	}
	return command.AppStatus{}, false
}

// statusWord names a status code for an error.
func statusWord(c command.ContainerStatusCode) string {
	switch c {
	case command.ContainerStatusRestarting:
		return "restarting"
	case command.ContainerStatusProblematic:
		return "a container exited with an error"
	case command.ContainerStatusStopped:
		return "stopped"
	}
	return "status unknown"
}
