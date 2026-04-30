package server

import (
	"fmt"
	"strings"

	"maas-box/internal/model"
)

const (
	configLLMProviderID   = "config_llm_provider"
	configLLMProviderName = "config.toml"
)

func (s *Server) getConfiguredLLMProvider() (model.ModelProvider, error) {
	if s == nil || s.cfg == nil {
		return model.ModelProvider{}, fmt.Errorf("server config is nil")
	}
	apiURL := strings.TrimSpace(s.cfg.Server.AI.LLMAPIURL)
	modelName := strings.TrimSpace(s.cfg.Server.AI.LLMModel)
	if apiURL == "" || modelName == "" {
		return model.ModelProvider{}, fmt.Errorf("llm config missing in [Server.AI]: LLMAPIURL/LLMModel")
	}
	return model.ModelProvider{
		ID:      configLLMProviderID,
		Name:    configLLMProviderName,
		APIURL:  apiURL,
		APIKey:  strings.TrimSpace(s.cfg.Server.AI.LLMAPIKey),
		Model:   modelName,
		Enabled: true,
	}, nil
}
