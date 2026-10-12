package sharedvars

import (
	"testing"

	"winterflow/internal/infra/transport/bus"
)

// The usecase cannot import the bus (domain over infra), so it mirrors the
// hub's not-connected answer; this pins the two together.
func TestAgentNotConnectedMatchesBus(t *testing.T) {
	if agentNotConnected != bus.ErrAgentNotConnected {
		t.Fatalf("%q != %q", agentNotConnected, bus.ErrAgentNotConnected)
	}
}
