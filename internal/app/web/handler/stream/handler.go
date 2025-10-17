package server

import (
	"fmt"
	"net/http"
	"time"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type Handler struct {
	logger *logger.Logger
	stream port.StreamManager
}

type Deps struct {
	Logger        *logger.Logger
	StreamManager port.StreamManager
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		logger: d.Logger,
		stream: d.StreamManager,
	}
}

func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// @todo: replace with real user ID from auth context
	userID := r.Header.Get("X-User-ID")
	ch := h.stream.AddSession(userID)
	defer h.stream.RemoveSession(userID, ch)

	ctx := r.Context()
	for {
		select {
		case msg := <-ch:
			h.logger.Debug("Received a stream message", "userID", userID, "msg", msg)
			fmt.Fprintf(w, "%s\n\n", msg)
			flusher.Flush()
			time.Sleep(1 * time.Second)
		case <-ctx.Done():
			h.logger.Debug("Client disconnected", "userID", userID)
			return
		}
	}
}
