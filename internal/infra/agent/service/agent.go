package service

import "context"

type AgentService struct {
}

func NewAgentService() *AgentService {
	return &AgentService{}
}

func (as *AgentService) HasConfig() bool {
	return false
}

func (as *AgentService) GenerateConfig(ctx context.Context) error {
	return nil
}
