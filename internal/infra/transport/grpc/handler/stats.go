package handler

import (
	"context"
	"winterflow/pkg/logger"

	"google.golang.org/grpc/stats"
)

type logStatsHandler struct {
	log *logger.Logger
}

func NewLogStatsHandler(log *logger.Logger) stats.Handler {
	return &logStatsHandler{
		log: log,
	}
}

func (h *logStatsHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	h.log.Debug("New connection from remote address", "remote_addr", info.RemoteAddr)
	return ctx
}

func (h *logStatsHandler) HandleConn(ctx context.Context, s stats.ConnStats) {
	switch ev := s.(type) {
	case *stats.ConnBegin:
		h.log.Info("Connection established")
	case *stats.ConnEnd:
		_ = ev // mark as used to satisfy linter
		h.log.Info("Connection closed")
	}
}

func (h *logStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	h.log.Debug("RPC started", "method", info.FullMethodName)
	return ctx
}

func (h *logStatsHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
	switch ev := s.(type) {
	case *stats.Begin:
		h.log.Debug("RPC begin", "begin_time", ev.BeginTime)
	case *stats.End:
		if ev.Error != nil {
			h.log.Error("RPC end with error", "error", ev.Error)
		} else {
			h.log.Debug("RPC end successfully", "end_time", ev.EndTime)
		}
	}
}
