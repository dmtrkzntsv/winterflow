package hub

import "time"

// SetDrainCheckEvery shortens how often h reads its drain flag (tests only).
func SetDrainCheckEvery(h *Hub, d time.Duration) { h.drainCheckEvery = d }
