package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Evidence-only shim for issue #23. It is branch-gated and must never merge.
// init runs before bench-gemini-live main(), executes only the hard-allowlisted
// zero-cost 31B benchmark, then exits before any Gemini Live request can occur.
func init() {
	if strings.TrimSpace(os.Getenv("GITHUB_HEAD_REF")) != "test/23-rerun-31b" {
		return
	}
	if err := runIssue23Free31B(); err != nil {
		fmt.Fprintf(os.Stderr, "#23 31B evidence shim: %v\n", err)
		os.Exit(86)
	}
	fmt.Fprintln(os.Stderr, "#23 31B zero-cost evidence complete; stopping before Gemini Live provider lane")
	os.Exit(86)
}

func runIssue23Free31B() error {
	commit := issue23ArgValue("--commit")
	if commit == "" {
		return fmt.Errorf("missing --commit provenance")
	}
	runner := issue23ArgValue("--runner")
	if runner == "" {
		runner = "github-hosted/ubuntu-24.04/x86_64"
	}
	out := issue23ArgValue("--out")
	if out == "" {
		return fmt.Errorf("missing --out evidence path")
	}
	evidenceDir := filepath.Dir(out)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return err
	}
	policy := "ZERO-SPEND #23 31B EVIDENCE\n" +
		"model=gemma-4-31b-it\n" +
		"paid_capable_models=rejected_before_inference\n" +
		"priced_provider_tools=disabled\n" +
		"request_interval=15s\n" +
		"retries=none\n" +
		"corpus=synthetic_non_sensitive\n" +
		"gemini_live_lane=blocked_by_branch_init_shim\n"
	if err := os.WriteFile(filepath.Join(evidenceDir, "COST_POLICY.txt"), []byte(policy), 0o644); err != nil {
		return err
	}

	report := filepath.Join(evidenceDir, "gemma-4-31b-it.json")
	args := []string{
		"run", "-tags", "nolibopusfile", "./cmd/eval-free-tier",
		"-model", "gemma-4-31b-it",
		"-scenarios", "./eval/tool_action_corpus.jsonl",
		"-runs", "1",
		"-run-id", commit+"-gemma-4-31b-it",
		"-hardware", runner,
		"-source-commit", commit,
		"-out", report,
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = "backend"
	cmd.Env = append(os.Environ(), "COMPANION_FREE_TIER_MIN_REQUEST_INTERVAL=15s")
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
	if writeErr := os.WriteFile(filepath.Join(evidenceDir, "gemma-4-31b-it.exit"), []byte(fmt.Sprintf("%d\n", exitCode)), 0o644); writeErr != nil {
		return writeErr
	}
	if _, statErr := os.Stat(report); statErr != nil {
		return fmt.Errorf("31B benchmark report missing: %w", statErr)
	}
	return nil
}

func issue23ArgValue(name string) string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == name {
			return strings.TrimSpace(os.Args[i+1])
		}
	}
	return ""
}
