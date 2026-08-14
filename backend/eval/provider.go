package eval

import (
	"context"
	"fmt"
)

type Stage string

const (
	StagePrimary    Stage = "primary"
	StageEscalation Stage = "escalation"
)

type ProviderRequest struct {
	Scenario Scenario
	Run      int
	Stage    Stage
}

type ProviderResponse struct {
	Observation Observation
	Timing      Timing
	Warnings    []string
}

type Provider interface {
	Metadata() ProviderMetadata
	EvidenceClass() string
	Evaluate(context.Context, ProviderRequest) (ProviderResponse, error)
}

type MockProvider struct {
	metadata ProviderMetadata
}

func NewMockProvider(name string) *MockProvider {
	if name == "" {
		name = "deterministic-fixture"
	}
	return &MockProvider{metadata: ProviderMetadata{Name: name, Runtime: "offline-mock"}}
}

func NewMockProviderWithMetadata(metadata ProviderMetadata) *MockProvider {
	if metadata.Name == "" {
		metadata.Name = "deterministic-fixture"
	}
	metadata.Runtime = "offline-mock"
	metadata.Endpoint = ""
	return &MockProvider{metadata: metadata}
}

func (p *MockProvider) Metadata() ProviderMetadata { return p.metadata }
func (p *MockProvider) EvidenceClass() string      { return EvidenceClassSynthetic }

func (p *MockProvider) Evaluate(_ context.Context, req ProviderRequest) (ProviderResponse, error) {
	if req.Scenario.Mock == nil {
		return ProviderResponse{Warnings: []string{"mock response not configured; emitted an empty observation"}}, nil
	}
	fixture := &req.Scenario.Mock.Primary
	if req.Stage == StageEscalation {
		if req.Scenario.Mock.Escalation == nil {
			return ProviderResponse{Warnings: []string{"mock escalation response not configured; emitted an empty observation"}}, nil
		}
		fixture = req.Scenario.Mock.Escalation
	}
	if fixture.TotalUS < 0 || (fixture.TTFTUS != nil && *fixture.TTFTUS < 0) {
		return ProviderResponse{}, fmt.Errorf("scenario %s has negative mock latency", req.Scenario.ID)
	}
	if fixture.TTFTUS != nil && *fixture.TTFTUS > fixture.TotalUS {
		return ProviderResponse{}, fmt.Errorf("scenario %s mock TTFT exceeds total latency", req.Scenario.ID)
	}
	if fixture.Error != "" {
		return ProviderResponse{}, fmt.Errorf("mock provider: %s", fixture.Error)
	}
	return ProviderResponse{
		Observation: cloneObservation(fixture.Observation),
		Timing:      Timing{TTFTUS: cloneInt64(fixture.TTFTUS), TotalUS: fixture.TotalUS},
	}, nil
}

func cloneObservation(in Observation) Observation {
	out := in
	out.Packs = append([]string(nil), in.Packs...)
	out.RetrievedIDs = append([]string(nil), in.RetrievedIDs...)
	out.ToolCalls = append([]ToolCall(nil), in.ToolCalls...)
	for i := range out.ToolCalls {
		out.ToolCalls[i].Arguments = append([]byte(nil), in.ToolCalls[i].Arguments...)
	}
	out.Escalate = cloneBool(in.Escalate)
	if in.Usage != nil {
		usage := *in.Usage
		out.Usage = &usage
	}
	return out
}

func cloneInt64(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneBool(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
