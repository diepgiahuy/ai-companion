package server

import (
	"context"
	"testing"
	"time"

	"companion-server/internal/pipeline"
)

type capabilityTestAgent struct{}

func (capabilityTestAgent) Respond(context.Context, string, string) (string, error) {
	return "ok", nil
}

func (capabilityTestAgent) RespondRich(context.Context, string, string) (pipeline.AgentResult, error) {
	return pipeline.AgentResult{Text: "ok"}, nil
}

func (capabilityTestAgent) Stream(context.Context, string, string, func(pipeline.AgentStreamEvent) error) error {
	return nil
}

func TestGuardProviderComponentsPreservesAgentCapabilities(t *testing.T) {
	guard := newProviderCallGuard(providerTimeouts{ASR: time.Second, Agent: time.Second, TTS: time.Second})
	components := guardProviderComponents(pipeline.Components{Agent: capabilityTestAgent{}}, guard)
	if _, ok := components.Agent.(pipeline.RichAgent); !ok {
		t.Fatal("RichAgent capability was lost")
	}
	if _, ok := components.Agent.(pipeline.StreamingAgent); !ok {
		t.Fatal("StreamingAgent capability was lost")
	}
}
