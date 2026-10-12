package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"google.golang.org/grpc"
)

// fakeStream records what is sent on it.
type fakeStream struct {
	grpc.ClientStream
	mu   sync.Mutex
	sent []*proto.AgentMessage
	got  chan struct{}
}

func newFakeStream() *fakeStream { return &fakeStream{got: make(chan struct{}, 8)} }

func (f *fakeStream) Send(m *proto.AgentMessage) error {
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	f.got <- struct{}{}
	return nil
}
func (f *fakeStream) Recv() (*proto.ServerCommand, error) { select {} }

// gatedDispatcher answers once released and reports the ctx it ran on.
type gatedDispatcher struct {
	release chan struct{}
	ctxErr  chan error
}

func (g gatedDispatcher) Dispatch(ctx context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	<-g.release
	g.ctxErr <- ctx.Err()
	return &proto.ResponseEnvelope{RequestId: req.RequestId, Type: req.Type}
}

func newDetachedAgent(t *testing.T, wait time.Duration) (*Agent, context.CancelFunc, gatedDispatcher) {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	a := NewAgent(log, config.NewServerConfig("standalone"), "agent-1")
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.lifeCtx, a.detachedWait = life, wait
	g := gatedDispatcher{release: make(chan struct{}), ctxErr: make(chan error, 1)}
	a.SetDispatcher(g)
	return a, cancel, g
}

func (a *Agent) testStreamUp(s *fakeStream) {
	a.streamMutex.Lock()
	a.stream, a.streamActive = s, true
	a.markUpLocked()
	a.streamMutex.Unlock()
}

func (a *Agent) testStreamDown() {
	a.streamMutex.Lock()
	a.stream = nil
	a.markDownLocked()
	a.streamMutex.Unlock()
}

func await(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: " + what)
	}
}

// A detached command whose stream is replaced while it runs still delivers
// its reply, on the new stream, and runs on the agent's lifetime rather than
// the old stream's context.
func TestDetachedReplyGoesToTheNewStream(t *testing.T) {
	a, _, g := newDetachedAgent(t, time.Minute)
	s1, s2 := newFakeStream(), newFakeStream()
	a.testStreamUp(s1)
	streamCtx, cancelStream := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.handleRequest(streamCtx, s1, &proto.RequestEnvelope{RequestId: "run-1", Type: string(command.TypeBackupRun)})
		close(done)
	}()

	// The stream drops, and a reconnect is slow: the reply waits for it.
	cancelStream()
	a.testStreamDown()
	close(g.release)
	if err := <-g.ctxErr; err != nil {
		t.Fatalf("a detached command ran on the stream's context: %v", err)
	}
	select {
	case <-done:
		t.Fatal("the reply was sent or dropped with no stream")
	case <-time.After(100 * time.Millisecond):
	}
	a.testStreamUp(s2)
	await(t, done, "reply on the new stream")
	if len(s1.sent) != 0 || len(s2.sent) != 1 || s2.sent[0].GetResponse().GetRequestId() != "run-1" {
		t.Fatalf("s1 = %d messages, s2 = %+v", len(s1.sent), s2.sent)
	}
}

// With no reconnect the reply is dropped after the bound.
func TestDetachedReplyGivesUp(t *testing.T) {
	a, _, g := newDetachedAgent(t, 50*time.Millisecond)
	close(g.release)
	done := make(chan struct{})
	start := time.Now()
	go func() {
		a.handleRequest(context.Background(), nil, &proto.RequestEnvelope{RequestId: "run-1", Type: string(command.TypeBackupRun)})
		close(done)
	}()
	await(t, done, "the reply wait to end")
	if time.Since(start) > 3*time.Second {
		t.Fatal("the wait outlived its bound")
	}
}

// The agent stopping ends the wait at once.
func TestDetachedReplyStopsWithTheAgent(t *testing.T) {
	a, stop, g := newDetachedAgent(t, time.Hour)
	close(g.release)
	done := make(chan struct{})
	go func() {
		a.handleRequest(context.Background(), nil, &proto.RequestEnvelope{RequestId: "run-1", Type: string(command.TypeBackupRun)})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	stop()
	await(t, done, "the reply wait to end on stop")
}

// An ordinary command still answers on the stream it arrived on, even when
// another has taken over.
func TestOrdinaryReplyKeepsItsStream(t *testing.T) {
	a, _, g := newDetachedAgent(t, time.Minute)
	close(g.release)
	s1, s2 := newFakeStream(), newFakeStream()
	a.testStreamUp(s2)
	a.handleRequest(context.Background(), s1, &proto.RequestEnvelope{RequestId: "list-1", Type: string(command.TypeAppsList)})
	if len(s1.sent) != 1 || len(s2.sent) != 0 {
		t.Fatalf("s1 = %d, s2 = %d", len(s1.sent), len(s2.sent))
	}
}
