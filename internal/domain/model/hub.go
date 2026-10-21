package model

// Hub is one gRPC hub as it announces itself in the hub list. A hub is
// available while its announcement lives; there is no hubs table.
type Hub struct {
	ID              string `json:"id"`
	PublicAddr      string `json:"public_addr"`
	Version         string `json:"version"`
	Capacity        int    `json:"capacity"`
	ConnectedAgents int    `json:"connected_agents"`
	// Assigned is how many servers the API has assigned to the hub; filled
	// by the picker from the servers table, never announced.
	Assigned  int  `json:"-"`
	Accepting bool `json:"accepting"`
	Draining  bool `json:"draining"`
}

// ErrServerHasNoHub: the server was never assigned a hub, so no command can
// reach it; its agent has to be reinstalled (409).
var ErrServerHasNoHub = &classified{"server has no hub; reinstall the agent", ErrConflict}
