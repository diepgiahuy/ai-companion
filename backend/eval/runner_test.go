package eval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type failingProvider struct{}

func (failingProvider) Metadata() ProviderMetadata { return ProviderMetadata{Name: "failing"} }
func (failingProvider) EvidenceClass() string      { return EvidenceClassSynthetic }
func (failingProvider) Evaluate(context.Context, ProviderRequest) (ProviderResponse, error) {
	return ProviderResponse{Observation: Observation{}}, errors.New("provider unavailable")
}

func TestMockRunnerAggregatesLatencyQualityCostAndEscalation(t *testing.T) {
	escalate := true
	primaryTTFT, escalationTTFT := int64(100), int64(50)
	scenario := Scenario{
		ID: "hard", Kind: "escalation", Input: "hard task",
		MustPack: []string{"memory"},
		Expect:   Expectations{Escalate: &escalate},
		Mock: &MockCase{
			Primary: MockResponse{
				Observation: Observation{Packs: []string{"context"}, Escalate: &escalate, Usage: &Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110}},
				TTFTUS:      &primaryTTFT, TotalUS: 400,
			},
			Escalation: &MockResponse{
				Observation: Observation{Packs: []string{"memory"}, Usage: &Usage{InputTokens: 200, OutputTokens: 20, TotalTokens: 220}},
				TTFTUS:      &escalationTTFT, TotalUS: 300,
			},
		},
	}
	inputPrice, outputPrice := 1.0, 2.0
	config := RunnerConfig{
		Runs: 2, Primary: NewMockProvider("main"), Escalation: NewMockProvider("hard"),
		Pricing:      Pricing{InputUSDPerMillion: &inputPrice, OutputUSDPerMillion: &outputPrice},
		CorpusSource: "fixture.jsonl", CorpusSHA256: "abc",
	}
	report, err := Run(context.Background(), []Scenario{scenario}, config)
	if err != nil {
		t.Fatal(err)
	}
	if report.EvidenceClass != "synthetic" || report.Selection != "not_selected" || len(report.Trials) != 2 {
		t.Fatalf("report header=%+v", report)
	}
	trial := report.Trials[0]
	if trial.FinalStage != "escalation" || trial.OverallTiming.TotalUS != 700 || trial.OverallTiming.TTFTUS == nil || *trial.OverallTiming.TTFTUS != 450 {
		t.Fatalf("trial timing=%+v", trial)
	}
	if trial.Metrics.QualityDelta == nil || *trial.Metrics.QualityDelta != 1 {
		t.Fatalf("quality delta=%+v", trial.Metrics.QualityDelta)
	}
	if report.Summary.Latency.P50TotalUS == nil || *report.Summary.Latency.P50TotalUS != 700 {
		t.Fatalf("latency summary=%+v", report.Summary.Latency)
	}
	if report.Summary.Quality.TaskSuccessRate == nil || *report.Summary.Quality.TaskSuccessRate != 1 {
		t.Fatalf("quality summary=%+v", report.Summary.Quality)
	}
	if report.Summary.Escalation.Precision == nil || *report.Summary.Escalation.Precision != 1 || report.Summary.Escalation.Executed != 2 {
		t.Fatalf("escalation summary=%+v", report.Summary.Escalation)
	}
	if report.Summary.Cost.EstimatedUSD == nil || *report.Summary.Cost.EstimatedUSD != 0.00072 {
		t.Fatalf("cost summary=%+v", report.Summary.Cost)
	}

	first, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Run(context.Background(), []Scenario{scenario}, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("mock reports are not deterministic")
	}
}

func TestMockRunnerDoesNotInferAnswersFromExpectations(t *testing.T) {
	report, err := Run(context.Background(), []Scenario{{ID: "missing", Input: "hello", MustPack: []string{"note"}}}, RunnerConfig{
		Runs: 1, Primary: NewMockProvider("mock"), CorpusSource: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	trial := report.Trials[0]
	if len(trial.Primary.Observation.Packs) != 0 || len(trial.Primary.Warnings) == 0 {
		t.Fatalf("trial=%+v", trial)
	}
	if trial.Metrics.Final.TaskSuccess == nil || *trial.Metrics.Final.TaskSuccess {
		t.Fatalf("missing mock fixture must not fabricate success: %+v", trial.Metrics.Final)
	}
}

func TestRunnerNeverScoresProviderFailureAsSuccess(t *testing.T) {
	report, err := Run(context.Background(), []Scenario{{ID: "fallback", Input: "hello", Fallback: true}}, RunnerConfig{
		Runs: 1, Primary: failingProvider{}, CorpusSource: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	trial := report.Trials[0]
	if report.Summary.Failures != 1 || trial.Metrics.Final.TaskSuccess == nil || *trial.Metrics.Final.TaskSuccess {
		t.Fatalf("failure was promoted: trial=%+v summary=%+v", trial, report.Summary)
	}
	if report.Summary.Quality.TaskSuccessTrials != 0 || report.Summary.Quality.MeanScore != nil {
		t.Fatalf("failed trials polluted quality summary: %+v", report.Summary.Quality)
	}
}

func TestRunnerMarksUnavailableEscalation(t *testing.T) {
	escalate := true
	report, err := Run(context.Background(), []Scenario{{
		ID: "hard", Input: "hard", Expect: Expectations{Escalate: &escalate},
		Mock: &MockCase{Primary: MockResponse{Observation: Observation{Escalate: &escalate}}},
	}}, RunnerConfig{Runs: 1, Primary: NewMockProvider("mock"), CorpusSource: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Escalation.Unavailable != 1 || report.Trials[0].Metrics.Final.TaskSuccess == nil || *report.Trials[0].Metrics.Final.TaskSuccess {
		t.Fatalf("unavailable escalation=%+v", report)
	}
}

func TestRunnerRejectsMeasuredProviderWithoutRunProvenance(t *testing.T) {
	provider, err := NewOpenAIProvider(OpenAIConfig{
		Name: "local", Model: "candidate", Version: "sha256:model", Runtime: "mlx-lm 1", Region: "local",
		Endpoint: "http://127.0.0.1:8000/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Run(context.Background(), []Scenario{{ID: "one", Input: "hello"}}, RunnerConfig{Runs: 1, Primary: provider})
	if err == nil || err.Error() != "provider measurement metadata requires run_id" {
		t.Fatalf("provenance error=%v", err)
	}
}
