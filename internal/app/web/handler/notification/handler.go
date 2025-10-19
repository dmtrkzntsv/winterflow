package notification

import (
	"fmt"
	"net/http"
	"time"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type Handler struct {
	log *logger.Logger
	nm  port.NotificationManager
}

type Deps struct {
	Logger              *logger.Logger
	NotificationManager port.NotificationManager
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		log: d.Logger,
		nm:  d.NotificationManager,
	}
}

func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-nm")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// @todo: replace with real user ID from auth context
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "anonymous"
	}
	ch := h.nm.AddChannel(userID)
	defer h.nm.RemoveChannel(userID, ch)

	ctx := r.Context()
	for {
		select {
		case msg := <-ch:
			h.log.Debug("Received a nm message", "userID", userID, "msg", msg)
			fmt.Fprintf(w, "%s\n\n", msg)
			flusher.Flush()
			time.Sleep(1 * time.Second)
		case <-ctx.Done():
			return
		}
	}
}
