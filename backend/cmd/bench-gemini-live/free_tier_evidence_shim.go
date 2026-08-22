package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const evidenceRequestInterval = 15 * time.Second

// This evidence-only shim exists only on the #23 benchmark branch. It runs
// before bench-gemini-live main(), uses the workflow's existing GEMINI_TOKEN
// only with eval-free-tier's hard Gemma allowlist, then exits before any
// Gemini Live provider request can occur. This file must never be merged.
// The workflow branch guard treats this intentional stop as evidence-only.
func init() {
	if strings.TrimSpace(os.Getenv("GITHUB_HEAD_REF")) != "test/23-free-tier-direct" {
		return
	}
	if err := runFreeTierModelEvidence(); err != nil {
		fmt.Fprintf(os.Stderr, "#23 zero-cost evidence shim: %v\n", err)
		os.Exit(86)
	}
	fmt.Fprintln(os.Stderr, "#23 zero-cost evidence complete; intentionally stopping before Gemini Live provider lane")
	os.Exit(86)
}

func runFreeTierModelEvidence() error {
	commit := argValue("--commit")
	if commit == "" {
		return fmt.Errorf("missing --commit provenance")
	}
	runner := argValue("--runner")
	if runner == "" {
		runner = "github-hosted/ubuntu-24.04/x86_64"
	}
	out := argValue("--out")
	if out == "" {
		return fmt.Errorf("missing --out evidence path")
	}
	evidenceDir := filepath.Dir(out)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return err
	}
	policy := "ZERO-SPEND #23 EVIDENCE\n" +
		"allowed_models=gemma-4-26b-a4b-it,gemma-4-31b-it\n" +
		"paid_capable_models=rejected_before_inference\n" +
		"priced_provider_tools=disabled\n" +
		"retries=none\n" +
		"request_pacing=15s_between_inference_request_starts\n" +
		"pacing_basis=conservative_project_observation_after_429_not_official_universal_rpm\n" +
		"corpus=synthetic_non_sensitive\n" +
		"paid_gemini_live_lane=blocked_by_branch_init_shim\n"
	if err := os.WriteFile(filepath.Join(evidenceDir, "COST_POLICY.txt"), []byte(policy), 0o644); err != nil {
		return err
	}

	models := []string{"gemma-4-26b-a4b-it", "gemma-4-31b-it"}
	var missingReports []string
	for i, model := range models {
		if i > 0 {
			time.Sleep(evidenceRequestInterval)
		}
		report := filepath.Join(evidenceDir, model+".json")
		args := []string{
			"run", "-tags", "nolibopusfile", "./cmd/eval-free-tier",
			"-model", model,
			"-scenarios", "./eval/tool_action_corpus.jsonl",
			"-runs", "1",
			"-run-id", commit+"-"+model,
			"-hardware", runner,
			"-source-commit", commit,
			"-out", report,
		}
		cmd := exec.Command("go", args...)
		cmd.Dir = "backend"
		cmd.Env = append(os.Environ(), freeTierPacingEnv+"="+evidenceRequestInterval.String())
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 127
			}
		}
		if writeErr := os.WriteFile(filepath.Join(evidenceDir, model+".exit"), []byte(fmt.Sprintf("%d\n", exitCode)), 0o644); writeErr != nil {
			return writeErr
		}
		if _, statErr := os.Stat(report); statErr != nil {
			missingReports = append(missingReports, model)
		}
	}
	if len(missingReports) > 0 {
		return fmt.Errorf("benchmark report missing for: %s", strings.Join(missingReports, ", "))
	}
	return nil
}

func argValue(name string) string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == name {
			return strings.TrimSpace(os.Args[i+1])
		}
	}
	return ""
}
