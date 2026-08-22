package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Evidence-only issue #23 shim. This file is branch-gated and must never merge.
// init runs before bench-gemini-live main(), executes only the zero-spend Gemma
// ADK/ToolRegistry compatibility probe, then exits before Gemini Live is called.
func init() {
	if strings.TrimSpace(os.Getenv("GITHUB_HEAD_REF")) != "test/23-adk-live-evidence" {
		return
	}
	if err := runIssue23ADKFreeEvidence(); err != nil {
		fmt.Fprintf(os.Stderr, "#23 ADK free-tier evidence shim: %v\n", err)
		os.Exit(86)
	}
	fmt.Fprintln(os.Stderr, "#23 ADK zero-spend evidence complete; stopping before Gemini Live provider lane")
	os.Exit(86)
}

func runIssue23ADKFreeEvidence() error {
	commit := issue23ADKArgValue("--commit")
	if commit == "" {
		return fmt.Errorf("missing --commit provenance")
	}
	runner := issue23ADKArgValue("--runner")
	if runner == "" {
		runner = "github-hosted/ubuntu-24.04/x86_64"
	}
	out := issue23ADKArgValue("--out")
	if out == "" {
		return fmt.Errorf("missing --out evidence path")
	}
	evidenceDir := filepath.Dir(out)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return err
	}
	policy := "ZERO-SPEND #23 PRODUCTION ADK EVIDENCE\n" +
		"model=gemma-4-31b-it\n" +
		"provider_base=https://generativelanguage.googleapis.com/v1beta/openai\n" +
		"protocol=chat_completions\n" +
		"runtime=NewProvider->ADK->ToolRegistry\n" +
		"provider_tool_aliases=true\n" +
		"tool=evidence.lookup(read-only)\n" +
		"request_interval=15s\n" +
		"retries=none\n" +
		"corpus=synthetic_non_sensitive\n" +
		"gemini_live_lane=blocked_by_branch_init_shim\n"
	if err := os.WriteFile(filepath.Join(evidenceDir, "ADK_COST_POLICY.txt"), []byte(policy), 0o644); err != nil {
		return err
	}

	apiKey := strings.TrimSpace(os.Getenv("GEMINI_TOKEN"))
	if apiKey == "" {
		return fmt.Errorf("GEMINI_TOKEN is required")
	}
	report := filepath.Join(evidenceDir, "adk-free-tier.json")
	cmd := exec.Command(
		"go", "run", "-tags", "adk,nolibopusfile", "./cmd/eval-adk-free-tier",
		"-source-commit", commit,
		"-hardware", runner,
		"-out", report,
	)
	cmd.Dir = "backend"
	cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+apiKey)
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
	if writeErr := os.WriteFile(filepath.Join(evidenceDir, "adk-free-tier.exit"), []byte(fmt.Sprintf("%d\n", exitCode)), 0o644); writeErr != nil {
		return writeErr
	}
	if _, statErr := os.Stat(report); statErr != nil {
		return fmt.Errorf("ADK evidence report missing: %w", statErr)
	}
	if err != nil {
		return fmt.Errorf("ADK evidence probe exited %d", exitCode)
	}
	return nil
}

func issue23ADKArgValue(name string) string {
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == name {
			return strings.TrimSpace(os.Args[i+1])
		}
	}
	return ""
}
