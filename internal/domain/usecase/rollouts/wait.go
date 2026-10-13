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
// after since shows the app healthy, and returns the last status seen for
// it. The app-level code alone cannot tell: the agent aggregates a container
// that is restarting (a crash loop under a restart policy) to Idle, and an
// app with a finished one-shot container (a migration job, exit 0) is Idle
// forever. So health is judged from the containers (see judgeApp): healthy
// reports pass at once. At the bound the step fails when no report came
// after it, the app has no containers, a container is restarting or exited
// with an error, or the app reads Stopped, Problematic or unknown; an app
// that reads Idle for other reasons (paused or created containers beside
// running ones) passes. A Docker healthcheck's "unhealthy" is not seen: such
// a container reads running.
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
			if listed && judgeApp(last).healthy {
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
	}
	j := judgeApp(last)
	switch {
	case j.reason != "":
		return last, fmt.Errorf("%s %s later", j.reason, uc.health)
	case last.StatusCode == command.ContainerStatusIdle:
		return last, nil
	}
	return last, fmt.Errorf("the app is not running %s later (%s)", uc.health, statusWord(last.StatusCode))
}

// judgment is a report's verdict on an app: healthy, or the reason it is
// surely not (empty while undecided).
type judgment struct {
	healthy bool
	reason  string
}

// judgeApp reads an app's containers. Healthy: at least one container, at
// least one Active, every container Active or Stopped (a finished one-shot).
// An app the agent aggregated to Active is healthy. A container restarting
// or exited with an error is named as the reason.
func judgeApp(a command.AppStatus) judgment {
	for _, c := range a.Containers {
		switch c.StatusCode {
		case command.ContainerStatusRestarting:
			return judgment{reason: fmt.Sprintf("container %s is restarting", c.Name)}
		case command.ContainerStatusProblematic:
			return judgment{reason: fmt.Sprintf("container %s exited with code %d", c.Name, c.ExitCode)}
		}
	}
	if a.StatusCode == command.ContainerStatusActive {
		return judgment{healthy: true}
	}
	if len(a.Containers) == 0 {
		return judgment{reason: "the app has no containers"}
	}
	active := false
	for _, c := range a.Containers {
		switch c.StatusCode {
		case command.ContainerStatusActive:
			active = true
		case command.ContainerStatusStopped:
		default:
			return judgment{}
		}
	}
	return judgment{healthy: active}
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
