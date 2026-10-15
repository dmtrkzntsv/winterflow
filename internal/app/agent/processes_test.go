package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
)

type fakeProcSource struct {
	resp  command.ProcessListResponse
	err   error
	block chan struct{}
}

func (f *fakeProcSource) List() (command.ProcessListResponse, error) {
	if f.block != nil {
		<-f.block
	}
	return f.resp, f.err
}

type fixedStatus struct{ apps []command.AppStatus }

func (f fixedStatus) StatusReport(context.Context) (command.GetAppsStatusResponse, error) {
	return command.GetAppsStatusResponse{Apps: f.apps}, nil
}

func TestProcessListerMapsContainersToApps(t *testing.T) {
	snap := NewStatusSnapshot(fixedStatus{apps: []command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{ContainerID: "c1"}}},
	}})
	if _, err := snap.StatusReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := &fakeProcSource{resp: command.ProcessListResponse{Processes: []command.Process{
		{PID: 1}, {PID: 2, ContainerID: "c1"}, {PID: 3, ContainerID: "unknown"},
	}}}
	l := NewProcessLister(src, snap)
	l.goos = "linux"
	resp, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Processes[0].AppID != "" || resp.Processes[1].AppID != "app-1" || resp.Processes[2].AppID != "" {
		t.Fatalf("apps = %+v", resp.Processes)
	}
}

func TestProcessListerNoSnapshotYet(t *testing.T) {
	src := &fakeProcSource{resp: command.ProcessListResponse{Processes: []command.Process{{PID: 2, ContainerID: "c1"}}}}
	l := NewProcessLister(src, NewStatusSnapshot(fixedStatus{}))
	l.goos = "linux"
	if resp, err := l.List(context.Background()); err != nil || resp.Processes[0].AppID != "" {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func TestProcessListerNotLinux(t *testing.T) {
	l := NewProcessLister(&fakeProcSource{}, nil)
	l.goos = "darwin"
	if _, err := l.List(context.Background()); !errors.Is(err, errProcessesUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessListerHonoursDeadline(t *testing.T) {
	src := &fakeProcSource{block: make(chan struct{})}
	defer close(src.block)
	l := NewProcessLister(src, nil)
	l.goos = "linux"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.List(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestDispatcherProcessList(t *testing.T) {
	d := newTestDispatcher(t)
	// Unwired: a clear error, not a panic.
	if resp := dispatchJSON(t, d, command.TypeProcessList, command.ProcessListRequest{}); resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("unwired process.list succeeded: %+v", resp.Base)
	}
	l := NewProcessLister(&fakeProcSource{resp: command.ProcessListResponse{NCPU: 4, Total: 1,
		Processes: []command.Process{{PID: 7, Name: "x"}}}}, nil)
	l.goos = "linux"
	d.SetProcesses(l)
	resp := dispatchJSON(t, d, command.TypeProcessList, command.ProcessListRequest{})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("process.list = %+v", resp.Base)
	}
	var out command.ProcessListResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.NCPU != 4 || len(out.Processes) != 1 || out.Processes[0].PID != 7 {
		t.Fatalf("payload %s err %v", resp.Payload, err)
	}
}
