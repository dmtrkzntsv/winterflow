package command

// agent.update self-update.

// UpdateAgentRequest asks the agent to replace its binary with the given
// version. The agent downloads the matching release, swaps the executable, and
// exits so its supervisor (systemd, or the Run reconnect loop's process manager)
// restarts it on the new version. An empty Version means "latest".
type UpdateAgentRequest struct {
	Version string `json:"version"`
}

type UpdateAgentResponse struct {
	// FromVersion is the version that was running when the update was accepted.
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	// Scheduled is true when the agent accepted the update and will exit to
	// apply it; false (with no error) means it was already up to date.
	Scheduled bool `json:"scheduled"`
}
