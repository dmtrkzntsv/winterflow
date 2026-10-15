package command

import (
	"encoding/json"
	"strings"
	"testing"
)

// The web client reads exactly these fields: disk rates live on groups,
// a process only names its group.
func TestProcessListResponseJSON(t *testing.T) {
	w := 4000.0
	b, err := json.Marshal(ProcessListResponse{
		Processes: []Process{{PID: 1, Group: "/system.slice/postgresql.service"}, {PID: 2}},
		Groups: []ProcessGroup{
			{ID: "/system.slice/postgresql.service", Label: "postgresql.service", Kind: "service", Processes: 1, WriteBps: &w},
			{ID: "/system.slice/docker-x.scope", Kind: "container", ContainerID: "x", AppID: "app-1", Processes: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"group":"/system.slice/postgresql.service"`,
		`"groups":[{"id":"/system.slice/postgresql.service","label":"postgresql.service","kind":"service","processes":1,"read_bps":null,"write_bps":4000}`,
		`{"id":"/system.slice/docker-x.scope","label":"","kind":"container","container_id":"x","app_id":"app-1","processes":3,"read_bps":null,"write_bps":null}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	var raw struct {
		Processes []map[string]any `json:"processes"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, p := range raw.Processes {
		for _, gone := range []string{"read_bps", "write_bps"} {
			if _, ok := p[gone]; ok {
				t.Errorf("process still has %s: %v", gone, p)
			}
		}
	}
	if _, ok := raw.Processes[1]["group"]; ok {
		t.Errorf("an ungrouped process has a group: %v", raw.Processes[1])
	}
}
