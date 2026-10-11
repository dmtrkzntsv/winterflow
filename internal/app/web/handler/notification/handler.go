package notification

import (
	"fmt"
	"net/http"
	webutil "winterflow/internal/app/web/util"
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
	userID, err := webutil.GetUserID(r)
	if err != nil || userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch := h.nm.AddChannel(userID)
	defer h.nm.RemoveChannel(userID, ch)

	// Send the headers now: once the client sees 200 it is subscribed, so
	// a command it dispatches next cannot finish before its result has a
	// channel to land in.
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case msg := <-ch:
			h.log.Debug("sending notification", "userID", userID)
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
