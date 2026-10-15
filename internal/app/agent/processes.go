package agent

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"winterflow/internal/domain/command"
)

// processListTimeout bounds a process.list's wait for its read (the
// standalone bridge runs it on a goroutine of its own, command.OffBridge).
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

// ProcessLister reads the processes and names each container's app, on
// processes and on disk I/O groups, from the status reporter's last
// snapshot (no docker call of its own). A container group's label is the
// container's name there, so the UI tells an app's containers apart. At most one read runs at a time:
// a /proc read can block for good on a hung process (cmdline takes its
// mmap_lock), and the calls behind it wait for that read instead of each
// starting a goroutine that would hang too.
type ProcessLister struct {
	src    ProcessSource
	status *StatusSnapshot
	goos   string

	mu     sync.Mutex
	flight *procFlight // the read in flight, nil when none
	joined func()      // test hook: a call joined the read in flight
}

// procFlight is one read: resp and err are set before done is closed.
type procFlight struct {
	done chan struct{}
	resp command.ProcessListResponse
	err  error
}

// NewProcessLister reads through src; status may be nil (no app names).
func NewProcessLister(src ProcessSource, status *StatusSnapshot) *ProcessLister {
	return &ProcessLister{src: src, status: status, goos: runtime.GOOS}
}

// List reads the processes, giving up when ctx ends (the read goes on in
// the background; the next call waits for it rather than reading again).
func (l *ProcessLister) List(ctx context.Context) (command.ProcessListResponse, error) {
	if l.goos != "linux" {
		return command.ProcessListResponse{}, errProcessesUnsupported
	}
	f := l.read()
	select {
	case <-f.done:
	case <-ctx.Done():
		return command.ProcessListResponse{}, ctx.Err()
	}
	if f.err != nil {
		return command.ProcessListResponse{}, f.err
	}
	// Every waiter gets the same answer: copy the rows before naming apps.
	resp := f.resp
	resp.Processes = append([]command.Process(nil), f.resp.Processes...)
	resp.Groups = append(make([]command.ProcessGroup, 0, len(f.resp.Groups)), f.resp.Groups...)
	if l.status != nil {
		if apps, ok := l.status.Latest(); ok {
			type container struct{ appID, name string }
			owner := map[string]container{}
			for _, a := range apps {
				for _, c := range a.Containers {
					owner[c.ContainerID] = container{appID: a.AppID, name: c.Name}
				}
			}
			for i := range resp.Processes {
				if id := resp.Processes[i].ContainerID; id != "" {
					resp.Processes[i].AppID = owner[id].appID
				}
			}
			for i := range resp.Groups {
				if id := resp.Groups[i].ContainerID; id != "" {
					c := owner[id]
					resp.Groups[i].AppID, resp.Groups[i].Label = c.appID, c.name
				}
			}
		}
	}
	return resp, nil
}

// read joins the read in flight or starts one.
func (l *ProcessLister) read() *procFlight {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.flight; f != nil {
		if l.joined != nil {
			l.joined()
		}
		return f
	}
	f := &procFlight{done: make(chan struct{})}
	l.flight = f
	go func() {
		f.resp, f.err = l.src.List()
		l.mu.Lock()
		l.flight = nil
		l.mu.Unlock()
		close(f.done)
	}()
	return f
}
