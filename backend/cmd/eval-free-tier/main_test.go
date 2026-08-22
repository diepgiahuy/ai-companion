package main

import (
	"path/filepath"
	"testing"
	"time"

	evalharness "companion-server/eval"
)

func TestValidateConfigRejectsBillableModel(t *testing.T) {
	if err := validateConfig("gemini-3.7-flash", "sha", "run", "runner", 1, time.Second); err == nil {
		t.Fatal("billable-capable Gemini model must be rejected")
	}
	if err := validateConfig("gemma-4-26b-a4b-it", "sha", "run", "runner", 1, time.Second); err != nil {
		t.Fatalf("free-only Gemma model rejected: %v", err)
	}
}

func TestCanonicalToolDefinitionsComeFromRegistry(t *testing.T) {
	definitions, digest, cleanup, err := canonicalToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if digest == "" {
		t.Fatal("tool schema digest is required")
	}
	known := map[string]bool{}
	for _, definition := range definitions {
		known[definition.Function.Name] = true
	}
	for _, name := range []string{"expense.log", "budget.set", "saving.goal_set", "note.create", "journal.create", "timer.create", "reminder.create", "memory.remember", "memory.recall", "memory.forget"} {
		if !known[name] {
			t.Fatalf("canonical ToolRegistry definition %q missing", name)
		}
	}
}

func TestToolActionCorpusHydratesCanonicalSchemas(t *testing.T) {
	path := filepath.Join("..", "..", "eval", "tool_action_corpus.jsonl")
	corpus, _, err := loadCorpus(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) != 30 {
		t.Fatalf("scenario count=%d want=30", len(corpus))
	}
	definitions, _, cleanup, err := canonicalToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := hydrateAndValidateCorpus(corpus, definitions); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range corpus {
		if len(scenario.Tools) != len(definitions) {
			t.Fatalf("scenario %s tool count=%d want=%d", scenario.ID, len(scenario.Tools), len(definitions))
		}
	}
}

func TestStrictEvidenceGateRejectsAnyUnsafeTrial(t *testing.T) {
	pass := true
	fail := false
	report := evalharness.Report{
		EvidenceClass: evalharness.EvidenceClassProviderMeasured,
		Trials: []evalharness.TrialResult{{Metrics: evalharness.TrialMetrics{Final: evalharness.QualityMetrics{TaskSuccess: &pass}}}},
	}
	if err := strictEvidenceGate(report); err != nil {
		t.Fatalf("safe report rejected: %v", err)
	}
	report.Trials = append(report.Trials, evalharness.TrialResult{Metrics: evalharness.TrialMetrics{Final: evalharness.QualityMetrics{TaskSuccess: &fail}}})
	if err := strictEvidenceGate(report); err == nil {
		t.Fatal("unsafe report must fail")
	}
}
