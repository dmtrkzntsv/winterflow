package dockercompose

import "winterflow/internal/domain/command"

// composePS mirrors one line of `docker compose ps --format json` output.
type composePS struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	State    string `json:"State"`    // running | exited | restarting | created | paused | dead
	ExitCode int    `json:"ExitCode"` // populated when State == exited
}

// mapContainerState maps a docker compose container State string to the
// command.ContainerStatusCode the API understands.
func mapContainerState(state string, exitCode int) command.ContainerStatusCode {
	switch state {
	case "running":
		return command.ContainerStatusActive
	case "restarting":
		return command.ContainerStatusRestarting
	case "paused", "created":
		return command.ContainerStatusIdle
	case "exited", "dead":
		if exitCode != 0 {
			return command.ContainerStatusProblematic
		}
		return command.ContainerStatusStopped
	default:
		return command.ContainerStatusUnknown
	}
}

// aggregateStatus derives an app's overall status from its containers, applying
// v1's precedence: any problematic container makes the app problematic; all
// running is active; all stopped is stopped; otherwise idle.
func aggregateStatus(containers []command.ContainerStatus) command.ContainerStatusCode {
	if len(containers) == 0 {
		return command.ContainerStatusUnknown
	}
	running, stopped, problematic := 0, 0, 0
	for _, c := range containers {
		switch c.StatusCode {
		case command.ContainerStatusProblematic:
			problematic++
		case command.ContainerStatusActive:
			running++
		case command.ContainerStatusStopped:
			stopped++
		}
	}
	switch {
	case problematic > 0:
		return command.ContainerStatusProblematic
	case running == len(containers):
		return command.ContainerStatusActive
	case stopped == len(containers):
		return command.ContainerStatusStopped
	default:
		return command.ContainerStatusIdle
	}
}

// toContainerStatuses converts compose ps lines into the command model.
func toContainerStatuses(lines []composePS) []command.ContainerStatus {
	out := make([]command.ContainerStatus, 0, len(lines))
	for _, l := range lines {
		out = append(out, command.ContainerStatus{
			ContainerID: l.ID,
			Name:        l.Name,
			StatusCode:  mapContainerState(l.State, l.ExitCode),
			ExitCode:    int32(l.ExitCode),
		})
	}
	return out
}
