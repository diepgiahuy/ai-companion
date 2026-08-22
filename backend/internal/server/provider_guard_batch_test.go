package server

import (
	"context"
	"testing"

	"companion-server/internal/pipeline"
)

func TestGuardProviderComponentsSuppressesStreamingForBatchSpeech(t *testing.T) {
	agent := testStreamingRichAgent{}
	components := guardProviderComponents(pipeline.Components{
		ASR:   testBatchASR{},
		Agent: agent,
		TTS:   testBatchTTS{},
	}, newProviderCallGuard(providerTimeouts{}))

	if _, ok := components.Agent.(pipeline.StreamingAgent); ok {
		t.Fatal("batch speech profile must not expose StreamingAgent")
	}
	if _, ok := components.Agent.(pipeline.RichAgent); !ok {
		t.Fatal("batch speech profile must preserve RichAgent")
	}
}

func TestGuardProviderComponentsKeepsStreamingForOrdinarySpeech(t *testing.T) {
	components := guardProviderComponents(pipeline.Components{
		ASR:   testBatchASR{},
		Agent: testStreamingRichAgent{},
		TTS:   testOrdinaryTTS{},
	}, newProviderCallGuard(providerTimeouts{}))

	if _, ok := components.Agent.(pipeline.StreamingAgent); !ok {
		t.Fatal("ordinary speech profile must preserve StreamingAgent")
	}
	if _, ok := components.Agent.(pipeline.RichAgent); !ok {
		t.Fatal("ordinary speech profile must preserve RichAgent")
	}
}

type testBatchASR struct{}

func (testBatchASR) Transcribe(context.Context, []byte) (string, error) { return "ok", nil }

type testBatchTTS struct{}

func (testBatchTTS) Synthesize(context.Context, string, func([]byte) error) error { return nil }
func (testBatchTTS) RequiresBatchAgent() bool                                    { return true }

type testOrdinaryTTS struct{}

func (testOrdinaryTTS) Synthesize(context.Context, string, func([]byte) error) error { return nil }

type testStreamingRichAgent struct{}

func (testStreamingRichAgent) Respond(context.Context, string, string) (string, error) {
	return "ok", nil
}

func (testStreamingRichAgent) RespondRich(context.Context, string, string) (pipeline.AgentResult, error) {
	return pipeline.AgentResult{Text: "ok"}, nil
}

func (testStreamingRichAgent) Stream(context.Context, string, string, func(pipeline.AgentStreamEvent) error) error {
	return nil
}
