package dockercompose

import (
	"encoding/json"
	"strconv"
	"strings"

	"winterflow/internal/domain/command"
)

// composePS is one container's status in compose terms.
type composePS struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	State    string `json:"State"`    // running | exited | restarting | created | paused | dead
	ExitCode int    `json:"ExitCode"` // populated when State == exited
}

// dockerPS mirrors one NDJSON line of `docker ps --format json` output.
// Status collection uses a single `docker ps` for ALL apps instead of one
// `docker compose ps` per app: forking the compose plugin per app per tick is
// the dominant idle-CPU cost on small machines.
type dockerPS struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	State  string `json:"State"`
	Status string `json:"Status"` // e.g. "Exited (137) 2 hours ago"
	Labels string `json:"Labels"` // comma-joined "k=v,k=v"
}

// composeProjectLabel is the label compose stamps on every container it owns.
const composeProjectLabel = "com.docker.compose.project"

// labelValue extracts one label from docker ps's comma-joined label string.
func labelValue(labels, key string) string {
	for _, kv := range strings.Split(labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// exitCodeFromStatus parses the code out of docker ps's human status,
// e.g. "Exited (137) 2 hours ago". 0 when absent or unparsable.
func exitCodeFromStatus(status string) int {
	const marker = "Exited ("
	i := strings.Index(status, marker)
	if i < 0 {
		return 0
	}
	rest := status[i+len(marker):]
	j := strings.IndexByte(rest, ')')
	if j < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return 0
	}
	return n
}

// parseDockerPS parses `docker ps --format json` NDJSON output and groups the
// containers by compose project name. Containers without a project label are
// skipped (they're not compose-managed).
func parseDockerPS(out []byte) (map[string][]composePS, error) {
	byProject := map[string][]composePS{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var c dockerPS
		if err := json.Unmarshal([]byte(l), &c); err != nil {
			return nil, err
		}
		project := labelValue(c.Labels, composeProjectLabel)
		if project == "" {
			continue
		}
		byProject[project] = append(byProject[project], composePS{
			ID:       c.ID,
			Name:     c.Names,
			State:    c.State,
			ExitCode: exitCodeFromStatus(c.Status),
		})
	}
	return byProject, nil
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
