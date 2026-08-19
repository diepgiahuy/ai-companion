package main

import (
	"testing"
)

func TestRunBenchmarksGeneratesSchemaCompliantReport(t *testing.T) {
	report := runBenchmarks("mock", "test-commit-sha")
	if report.SchemaVersion != "companion.speech.benchmark.v1" {
		t.Errorf("expected schema version companion.speech.benchmark.v1, got %s", report.SchemaVersion)
	}
	if report.SourceCommit != "test-commit-sha" {
		t.Errorf("expected source commit test-commit-sha, got %s", report.SourceCommit)
	}
	if len(report.Runs) != 1 {
		t.Fatalf("expected 1 run in mock mode, got %d", len(report.Runs))
	}
	run := report.Runs[0]
	if run.Lane != "mock" {
		t.Errorf("expected lane mock, got %s", run.Lane)
	}
	if run.Status != "passed" {
		t.Errorf("expected status passed, got %s", run.Status)
	}
	if run.EvidenceClass != "synthetic" {
		t.Errorf("expected evidence_class synthetic, got %s", run.EvidenceClass)
	}
	if run.ASRMetrics == nil || run.ASRMetrics.Transcript != "Tôi vừa chi 50 ngàn ăn trưa" {
		t.Errorf("unexpected ASR metrics: %+v", run.ASRMetrics)
	}
	if run.TTSMetrics == nil || run.TTSMetrics.PCMBytes <= 0 {
		t.Errorf("unexpected TTS metrics: %+v", run.TTSMetrics)
	}
	if run.Cancellation == nil || !run.Cancellation.CancelledSuccessfully {
		t.Errorf("expected successful cancellation, got %+v", run.Cancellation)
	}
}

func TestRunBenchmarksHandlesMissingCredentialsWithInsufficientEvidence(t *testing.T) {
	report := runBenchmarks("all", "test-commit-sha")
	if len(report.Runs) != 4 {
		t.Fatalf("expected 4 lanes evaluated, got %d", len(report.Runs))
	}
	if report.Summary.InsufficientLanes < 3 {
		t.Errorf("expected at least 3 insufficient evidence lanes due to missing credentials, got %d", report.Summary.InsufficientLanes)
	}
}
