package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	evalharness "companion-server/eval"
)

func TestRunMockIsDeterministicAndMachineReadable(t *testing.T) {
	directory := t.TempDir()
	corpus := filepath.Join(directory, "scenarios.jsonl")
	line := `{"id":"timer","input":"Hẹn giờ","must_pack":["schedule"],"mock":{"primary":{"observation":{"packs":["schedule"],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}},"ttft_us":10,"total_us":20}}}` + "\n"
	if err := os.WriteFile(corpus, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-mode", "mock", "-scenarios", corpus, "-runs", "2", "-input-usd-per-million", "1", "-output-usd-per-million", "2"}
	var first, second, stderr bytes.Buffer
	if code := run(args, &first, &stderr); code != 0 {
		t.Fatalf("first exit=%d stderr=%s", code, stderr.String())
	}
	stderr.Reset()
	if code := run(args, &second, &stderr); code != 0 {
		t.Fatalf("second exit=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("offline CLI output changed between identical runs")
	}
	var report evalharness.Report
	if err := json.Unmarshal(first.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.SchemaVersion != evalharness.ReportSchemaVersion || report.EvidenceClass != "synthetic" || report.Selection != "not_selected" {
		t.Fatalf("report header=%+v", report)
	}
	if report.Summary.Trials != 2 || report.Summary.Quality.TaskSuccessRate == nil || *report.Summary.Quality.TaskSuccessRate != 1 {
		t.Fatalf("summary=%+v", report.Summary)
	}
}

func TestOpenAIModeRequiresCompleteProvenance(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "openai", "-endpoint", "http://127.0.0.1:8000/v1", "-model", "candidate"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "requires -provider provenance") {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
}

func TestCLIExitsNonzeroWhenEscalationIsUnavailable(t *testing.T) {
	directory := t.TempDir()
	corpus := filepath.Join(directory, "scenarios.jsonl")
	line := `{"id":"hard","input":"hard","expect":{"escalate":true},"mock":{"primary":{"observation":{"escalate":true}}}}` + "\n"
	if err := os.WriteFile(corpus, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "mock", "-mock-escalation=false", "-scenarios", corpus}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "1 unavailable escalation") {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
}
