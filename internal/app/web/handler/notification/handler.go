package notification

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// Stream lifetime: a stream is authenticated once, at connect, so it ends
// after streamLifetime plus up to streamJitter (spreading reconnects) and the
// browser's EventSource reconnects through the auth middleware again. A
// revoked session therefore stops receiving events within this bound.
const (
	streamLifetime = 10 * time.Minute
	streamJitter   = time.Minute
	// reconnectMillis is the EventSource retry delay the stream announces,
	// keeping the gap of a planned reconnect short.
	reconnectMillis = 1000
)

type Handler struct {
	log *logger.Logger
	nm  port.NotificationManager
	// lifetime returns how long the next stream may stay open.
	lifetime func() time.Duration
}

type Deps struct {
	Logger              *logger.Logger
	NotificationManager port.NotificationManager
}

func NewHandler(d *Deps) *Handler {
	return &Handler{
		log: d.Logger,
		nm:  d.NotificationManager,
		lifetime: func() time.Duration {
			return streamLifetime + rand.N(streamJitter)
		},
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
	fmt.Fprintf(w, "retry: %d\n\n", reconnectMillis)
	flusher.Flush()

	end := time.NewTimer(h.lifetime())
	defer end.Stop()
	ctx := r.Context()
	for {
		select {
		case <-end.C:
			return
		case msg := <-ch:
			h.log.Debug("sending notification", "userID", userID)
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
