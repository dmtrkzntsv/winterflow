package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
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

func TestProcessListerMapsGroupsToApps(t *testing.T) {
	snap := NewStatusSnapshot(fixedStatus{apps: []command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{ContainerID: "c1"}}},
	}})
	if _, err := snap.StatusReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := &fakeProcSource{resp: command.ProcessListResponse{Groups: []command.ProcessGroup{
		{ID: "/system.slice/x.service", Kind: "service"},
		{ID: "/system.slice/docker-c1.scope", Kind: "container", ContainerID: "c1"},
		{ID: "/system.slice/docker-c2.scope", Kind: "container", ContainerID: "unknown"},
	}}}
	l := NewProcessLister(src, snap)
	l.goos = "linux"
	resp, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Groups[0].AppID != "" || resp.Groups[1].AppID != "app-1" || resp.Groups[2].AppID != "" {
		t.Fatalf("groups = %+v", resp.Groups)
	}
	// The read's own answer is left alone: later callers name apps afresh.
	if src.resp.Groups[1].AppID != "" {
		t.Fatal("the shared answer's groups were written")
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

// countingProcSource counts its reads and holds each one until a value
// arrives on release.
type countingProcSource struct {
	entered atomic.Int32
	release chan struct{}
}

func (c *countingProcSource) List() (command.ProcessListResponse, error) {
	n := c.entered.Add(1)
	<-c.release
	return command.ProcessListResponse{Total: int(n), Processes: []command.Process{{PID: int(n), ContainerID: "c1"}}}, nil
}

// waitIdle waits for the lister's read in flight, if any, to finish.
func waitIdle(t *testing.T, l *ProcessLister) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		l.mu.Lock()
		idle := l.flight == nil
		l.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the read never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

// A hung /proc read is read once: the calls behind it wait for it (each
// bounded by its own ctx) instead of piling up goroutines of their own.
func TestProcessListerOneReadInFlight(t *testing.T) {
	src := &countingProcSource{release: make(chan struct{})}
	l := NewProcessLister(src, nil)
	l.goos = "linux"
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			_, errs[i] = l.List(ctx)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("call %d: err = %v", i, err)
		}
	}
	if n := src.entered.Load(); n != 1 {
		t.Fatalf("source read %d times while one read hung", n)
	}
	src.release <- struct{}{}
	waitIdle(t, l)
	// The next call after the read finished starts a fresh one.
	go func() { src.release <- struct{}{} }()
	resp, err := l.List(context.Background())
	if err != nil || resp.Total != 2 || src.entered.Load() != 2 {
		t.Fatalf("resp %+v err %v reads %d", resp, err, src.entered.Load())
	}
}

// Every caller still waiting when the read ends gets its answer, each with
// its own copy of the rows (app names are filled in per call).
func TestProcessListerSharesTheReadInFlight(t *testing.T) {
	snap := NewStatusSnapshot(fixedStatus{apps: []command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{ContainerID: "c1"}}},
	}})
	if _, err := snap.StatusReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	src := &countingProcSource{release: make(chan struct{})}
	l := NewProcessLister(src, snap)
	l.goos = "linux"
	joined := make(chan struct{}, 2)
	l.joined = func() { joined <- struct{}{} }
	type result struct {
		resp command.ProcessListResponse
		err  error
	}
	out := make(chan result, 2)
	call := func() {
		resp, err := l.List(context.Background())
		out <- result{resp, err}
	}
	go call()
	for src.entered.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	go call()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("the second call never joined the read in flight")
	}
	src.release <- struct{}{}
	var got []result
	for range 2 {
		select {
		case r := <-out:
			got = append(got, r)
		case <-time.After(2 * time.Second):
			t.Fatal("a caller never got the read's answer")
		}
	}
	for _, r := range got {
		if r.err != nil || r.resp.Total != 1 || len(r.resp.Processes) != 1 || r.resp.Processes[0].AppID != "app-1" {
			t.Fatalf("resp %+v err %v", r.resp, r.err)
		}
	}
	if &got[0].resp.Processes[0] == &got[1].resp.Processes[0] {
		t.Fatal("the callers share one slice of rows")
	}
	if n := src.entered.Load(); n != 1 {
		t.Fatalf("source read %d times for one read", n)
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
