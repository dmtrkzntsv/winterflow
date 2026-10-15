package agent

import (
	"context"
	"errors"
	"runtime"
	"time"

	"winterflow/internal/domain/command"
)

// processListTimeout bounds a process.list: it runs on the standalone
// bridge's single goroutine, so it must never hang.
const processListTimeout = 10 * time.Second

var (
	errProcessesUnsupported = errors.New("process list is only available on Linux")
	errNoProcesses          = errors.New("process list is not available on this agent")
)

// ProcessSource reads the host's processes (sysmetrics.Processes).
type ProcessSource interface {
	List() (command.ProcessListResponse, error)
}

// Processes answers process.list (ProcessLister in production).
type Processes interface {
	List(ctx context.Context) (command.ProcessListResponse, error)
}

type noProcesses struct{}

func (noProcesses) List(context.Context) (command.ProcessListResponse, error) {
	return command.ProcessListResponse{}, errNoProcesses
}

// ProcessLister reads the processes and names each container's app from
// the status reporter's last snapshot (no docker call of its own).
type ProcessLister struct {
	src    ProcessSource
	status *StatusSnapshot
	goos   string
}

// NewProcessLister reads through src; status may be nil (no app names).
func NewProcessLister(src ProcessSource, status *StatusSnapshot) *ProcessLister {
	return &ProcessLister{src: src, status: status, goos: runtime.GOOS}
}

// List reads the processes, giving up when ctx ends (the read goes on in
// the background and its answer is dropped; file reads end on their own).
func (l *ProcessLister) List(ctx context.Context) (command.ProcessListResponse, error) {
	if l.goos != "linux" {
		return command.ProcessListResponse{}, errProcessesUnsupported
	}
	type result struct {
		resp command.ProcessListResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := l.src.List()
		ch <- result{resp, err}
	}()
	var r result
	select {
	case r = <-ch:
	case <-ctx.Done():
		return command.ProcessListResponse{}, ctx.Err()
	}
	if r.err != nil {
		return command.ProcessListResponse{}, r.err
	}
	if l.status != nil {
		if apps, ok := l.status.Latest(); ok {
			owner := map[string]string{}
			for _, a := range apps {
				for _, c := range a.Containers {
					owner[c.ContainerID] = a.AppID
				}
			}
			for i := range r.resp.Processes {
				if id := r.resp.Processes[i].ContainerID; id != "" {
					r.resp.Processes[i].AppID = owner[id]
				}
			}
		}
	}
	return r.resp, nil
}
