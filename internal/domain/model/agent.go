package model

type AgentConfig struct {
	ID               string
	Status           string
	Features         map[string]string
	AppsPath         string
	CertificatesPath string
}
