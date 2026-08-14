//go:build adk

package adkbridge

import (
	"context"
	"fmt"
	"strings"

	"companion-server/internal/pipeline"

	"google.golang.org/adk/v2/model/openaimodel"
)

// NewProvider is the single product LLM composition entrypoint. The transport
// protocol may vary per provider, but every choice still feeds the same ADK
// llmagent, Companion ToolRegistry, conversation store, policy and usage meter.
func NewProvider(cfg Config) (pipeline.Agent, error) {
	protocol := strings.ToLower(strings.TrimSpace(cfg.ModelProtocol))
	if protocol == "" {
		protocol = ModelProtocolResponses
	}

	var llm interfaceModel
	var err error
	switch protocol {
	case ModelProtocolResponses:
		llm, err = openaimodel.NewModel(context.Background(), cfg.ModelName, &openaimodel.ClientConfig{
			APIKey: cfg.APIKey, BaseURL: strings.TrimSpace(cfg.BaseURL), HTTPClient: cfg.HTTPClient,
		})
	case ModelProtocolChatCompletions:
		llm, err = newChatCompletionsModel(cfg)
	default:
		return nil, fmt.Errorf("unsupported ADK model protocol %q", cfg.ModelProtocol)
	}
	if err != nil {
		return nil, err
	}
	return newWithModel(cfg, withProviderToolAliases(llm))
}

// interfaceModel is the exact ADK model.LLM surface expressed locally so the
// factory stays small. Both official openaimodel and Companion chat adapter
// satisfy it; withProviderToolAliases accepts the same interface via assignment.
type interfaceModel = model.LLM
