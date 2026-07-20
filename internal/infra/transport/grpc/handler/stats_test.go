package handler

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/stats"

	"winterflow/pkg/logger"
)

// The stats handler is debug logging only; the contract worth pinning is that
// every callback is safe to invoke and TagConn/TagRPC pass the context through.
func TestLogStatsHandlerCallbacksAreSafe(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := NewLogStatsHandler(log)
	ctx := context.Background()

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
	if got := h.TagConn(ctx, &stats.ConnTagInfo{RemoteAddr: addr}); got != ctx {
		t.Fatal("TagConn must return the same context")
	}
	h.HandleConn(ctx, &stats.ConnBegin{})
	h.HandleConn(ctx, &stats.ConnEnd{})

	if got := h.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: "/svc/Method"}); got != ctx {
		t.Fatal("TagRPC must return the same context")
	}
	h.HandleRPC(ctx, &stats.Begin{})
	h.HandleRPC(ctx, &stats.End{})
	h.HandleRPC(ctx, &stats.InPayload{})
}
