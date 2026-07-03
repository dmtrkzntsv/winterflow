package dockercompose

import (
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
)

func TestParseRevisions(t *testing.T) {
	got := parseRevisions([]string{"3", "1", "x", "10", "2"})
	want := []uint32{1, 2, 3, 10}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseRevisions = %v, want %v", got, want)
	}
}

func TestNextRevision(t *testing.T) {
	if got := nextRevision(nil); got != 1 {
		t.Errorf("nextRevision(nil) = %d, want 1", got)
	}
	if got := nextRevision([]uint32{1, 2, 5}); got != 6 {
		t.Errorf("nextRevision = %d, want 6", got)
	}
}

func TestRevisionsToPrune(t *testing.T) {
	cases := []struct {
		name     string
		existing []uint32
		keep     int
		want     []uint32
	}{
		{"under limit", []uint32{1, 2}, 3, nil},
		{"at limit", []uint32{1, 2, 3}, 3, nil},
		{"over limit prunes oldest", []uint32{1, 2, 3, 4, 5}, 3, []uint32{1, 2}},
		{"unsorted input", []uint32{5, 1, 3, 2, 4}, 2, []uint32{1, 2, 3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := revisionsToPrune(c.existing, c.keep)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("revisionsToPrune(%v, %d) = %v, want %v", c.existing, c.keep, got, c.want)
			}
		})
	}
}

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

func TestParseComposePS(t *testing.T) {
	// Newline-delimited form (docker compose v2 default).
	ndjson := `{"ID":"abc","Name":"web","State":"running","ExitCode":0}
{"ID":"def","Name":"db","State":"exited","ExitCode":1}`
	lines, err := parseComposePS([]byte(ndjson))
	if err != nil {
		t.Fatalf("parseComposePS ndjson: %v", err)
	}
	if len(lines) != 2 || lines[0].Name != "web" || lines[1].ExitCode != 1 {
		t.Errorf("ndjson parse wrong: %+v", lines)
	}

	// Array form.
	arr := `[{"ID":"abc","Name":"web","State":"running","ExitCode":0}]`
	lines, err = parseComposePS([]byte(arr))
	if err != nil {
		t.Fatalf("parseComposePS array: %v", err)
	}
	if len(lines) != 1 || lines[0].State != "running" {
		t.Errorf("array parse wrong: %+v", lines)
	}

	// Empty output.
	lines, err = parseComposePS([]byte("  \n "))
	if err != nil || lines != nil {
		t.Errorf("empty parse: lines=%v err=%v", lines, err)
	}

	// Malformed array form.
	if _, err := parseComposePS([]byte("[{broken")); err == nil {
		t.Error("expected error for malformed array output")
	}

	// Malformed line in ndjson form.
	if _, err := parseComposePS([]byte("{\"ID\":\"abc\"}\n{broken")); err == nil {
		t.Error("expected error for malformed ndjson line")
	}
}
