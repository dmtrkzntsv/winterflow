package dockercompose

import (
	"testing"

	"winterflow/internal/domain/command"
)

func TestMapContainerState(t *testing.T) {
	cases := []struct {
		state    string
		exitCode int
		want     command.ContainerStatusCode
	}{
		{"running", 0, command.ContainerStatusActive},
		{"restarting", 0, command.ContainerStatusRestarting},
		{"paused", 0, command.ContainerStatusIdle},
		{"created", 0, command.ContainerStatusIdle},
		{"exited", 0, command.ContainerStatusStopped},
		{"exited", 1, command.ContainerStatusProblematic},
		{"dead", 137, command.ContainerStatusProblematic},
		{"bogus", 0, command.ContainerStatusUnknown},
	}
	for _, c := range cases {
		if got := mapContainerState(c.state, c.exitCode); got != c.want {
			t.Errorf("mapContainerState(%q,%d) = %v, want %v", c.state, c.exitCode, got, c.want)
		}
	}
}

func TestAggregateStatus(t *testing.T) {
	active := command.ContainerStatus{StatusCode: command.ContainerStatusActive}
	stopped := command.ContainerStatus{StatusCode: command.ContainerStatusStopped}
	problematic := command.ContainerStatus{StatusCode: command.ContainerStatusProblematic}

	cases := []struct {
		name string
		in   []command.ContainerStatus
		want command.ContainerStatusCode
	}{
		{"empty", nil, command.ContainerStatusUnknown},
		{"all active", []command.ContainerStatus{active, active}, command.ContainerStatusActive},
		{"all stopped", []command.ContainerStatus{stopped, stopped}, command.ContainerStatusStopped},
		{"any problematic", []command.ContainerStatus{active, problematic}, command.ContainerStatusProblematic},
		{"mixed", []command.ContainerStatus{active, stopped}, command.ContainerStatusIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aggregateStatus(c.in); got != c.want {
				t.Errorf("aggregateStatus = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseDockerPS(t *testing.T) {
	// One `docker ps --format json` NDJSON stream covering two compose
	// projects plus a non-compose container that must be skipped.
	ndjson := `{"ID":"abc","Names":"wf-app1-web-1","State":"running","Status":"Up 2 hours","Labels":"com.docker.compose.project=wf-app1,com.docker.compose.service=web"}
{"ID":"def","Names":"wf-app1-db-1","State":"exited","Status":"Exited (1) 3 hours ago","Labels":"com.docker.compose.service=db,com.docker.compose.project=wf-app1"}
{"ID":"ghi","Names":"wf-app2-api-1","State":"running","Status":"Up 5 minutes (healthy)","Labels":"com.docker.compose.project=wf-app2"}
{"ID":"zzz","Names":"standalone-nginx","State":"running","Status":"Up 1 hour","Labels":"maintainer=nginx"}`

	byProject, err := parseDockerPS([]byte(ndjson))
	if err != nil {
		t.Fatalf("parseDockerPS: %v", err)
	}
	if len(byProject) != 2 {
		t.Fatalf("want 2 projects, got %d: %+v", len(byProject), byProject)
	}
	app1 := byProject["wf-app1"]
	if len(app1) != 2 || app1[0].Name != "wf-app1-web-1" || app1[0].State != "running" {
		t.Errorf("wf-app1 parse wrong: %+v", app1)
	}
	if app1[1].ExitCode != 1 {
		t.Errorf("exit code not parsed from Status: %+v", app1[1])
	}
	if len(byProject["wf-app2"]) != 1 {
		t.Errorf("wf-app2 parse wrong: %+v", byProject["wf-app2"])
	}

	// Empty output.
	byProject, err = parseDockerPS([]byte("  \n "))
	if err != nil || len(byProject) != 0 {
		t.Errorf("empty parse: %v err=%v", byProject, err)
	}

	// Malformed line.
	if _, err := parseDockerPS([]byte("{\"ID\":\"abc\"}\n{broken")); err == nil {
		t.Error("expected error for malformed ndjson line")
	}
}

func TestExitCodeFromStatus(t *testing.T) {
	cases := []struct {
		status string
		want   int
	}{
		{"Exited (0) 2 hours ago", 0},
		{"Exited (137) 2 hours ago", 137},
		{"Up 2 hours", 0},
		{"Up 5 minutes (healthy)", 0},
		{"Exited (broken", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := exitCodeFromStatus(c.status); got != c.want {
			t.Errorf("exitCodeFromStatus(%q) = %d, want %d", c.status, got, c.want)
		}
	}
}

func TestLabelValue(t *testing.T) {
	labels := "a=1,com.docker.compose.project=wf-x,b=2"
	if got := labelValue(labels, "com.docker.compose.project"); got != "wf-x" {
		t.Errorf("labelValue = %q, want wf-x", got)
	}
	if got := labelValue(labels, "missing"); got != "" {
		t.Errorf("labelValue missing = %q, want empty", got)
	}
	if got := labelValue("", "k"); got != "" {
		t.Errorf("labelValue empty = %q, want empty", got)
	}
}
