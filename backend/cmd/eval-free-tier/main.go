package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	evalharness "companion-server/eval"
	"companion-server/internal/capability"
	"companion-server/internal/memory"
	"companion-server/internal/providers/tools"
	"companion-server/internal/store"
)

const promptVersion = "companion-tool-action-free-tier-v1"

var benchmarkPacks = []string{"expense", "budget", "saving", "note", "journal", "schedule", "memory"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval-free-tier", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var scenariosPath, outputPath, model, apiKeyEnv, baseURL, sourceCommit, runID, hardware string
	var runs int
	var timeout time.Duration
	flags.StringVar(&scenariosPath, "scenarios", "./eval/tool_action_corpus.jsonl", "tool-action JSONL corpus")
	flags.StringVar(&outputPath, "out", "-", "report JSON path or - for stdout")
	flags.StringVar(&model, "model", "", "zero-cost model id; only explicitly allowlisted Gemma 4 ids are accepted")
	flags.StringVar(&apiKeyEnv, "api-key-env", "GEMINI_TOKEN", "environment variable containing the Gemini API key")
	flags.StringVar(&baseURL, "base-url", "", "Gemini Developer API base URL; tests may use localhost")
	flags.StringVar(&sourceCommit, "source-commit", "", "exact benchmark source commit")
	flags.StringVar(&runID, "run-id", "", "stable benchmark run id")
	flags.StringVar(&hardware, "hardware", "", "measured runner hardware description")
	flags.IntVar(&runs, "runs", 1, "ordered repetitions of each scenario")
	flags.DurationVar(&timeout, "request-timeout", 90*time.Second, "per-provider-request timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if err := validateConfig(model, sourceCommit, runID, hardware, runs, timeout); err != nil {
		fmt.Fprintf(stderr, "configuration: %v\n", err)
		return 2
	}
	apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if apiKey == "" {
		fmt.Fprintf(stderr, "configuration: %s is required\n", apiKeyEnv)
		return 2
	}

	corpus, corpusHash, err := loadCorpus(scenariosPath)
	if err != nil {
		fmt.Fprintf(stderr, "load corpus: %v\n", err)
		return 2
	}
	toolDefinitions, schemaDigest, cleanup, err := canonicalToolDefinitions()
	if err != nil {
		fmt.Fprintf(stderr, "build canonical tool definitions: %v\n", err)
		return 1
	}
	defer cleanup()
	if err := hydrateAndValidateCorpus(corpus, toolDefinitions); err != nil {
		fmt.Fprintf(stderr, "validate corpus: %v\n", err)
		return 2
	}

	client := &http.Client{Timeout: timeout}
	version, err := evalharness.ResolveGemmaFreeModelVersion(context.Background(), baseURL, apiKey, model, client)
	if err != nil {
		fmt.Fprintf(stderr, "resolve model version: %v\n", err)
		return 1
	}
	provider, err := evalharness.NewGemmaFreeProvider(evalharness.GemmaFreeConfig{
		Model: model, Version: version, APIKey: apiKey, BaseURL: baseURL, HTTPClient: client,
	})
	if err != nil {
		fmt.Fprintf(stderr, "configure provider: %v\n", err)
		return 2
	}

	report, err := evalharness.Run(context.Background(), corpus, evalharness.RunnerConfig{
		Runs:         runs,
		Primary:      provider,
		CorpusSource: scenariosPath,
		CorpusSHA256: corpusHash,
		Metadata: evalharness.RunMetadata{
			RunID:            runID,
			Hardware:         hardware,
			RuntimeConfig:    "zero_cost=required;provider=gemma4_free_only;paid_tier=unavailable;retry=none;temperature=0;max_output_tokens=512;tool_schema_sha256=" + schemaDigest,
			PromptVersion:    promptVersion,
			ToolSchemaCommit: sourceCommit,
			SourceCommit:     sourceCommit,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "run benchmark: %v\n", err)
		return 1
	}
	if err := writeReport(outputPath, stdout, report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if err := strictEvidenceGate(report); err != nil {
		fmt.Fprintf(stderr, "benchmark evidence gate: %v\n", err)
		return 1
	}
	return 0
}

func validateConfig(model, sourceCommit, runID, hardware string, runs int, timeout time.Duration) error {
	if !evalharness.IsFreeOnlyGemmaModel(model) {
		return fmt.Errorf("model %q is not allowed by the zero-cost benchmark", model)
	}
	if strings.TrimSpace(sourceCommit) == "" {
		return errors.New("source-commit is required")
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("run-id is required")
	}
	if strings.TrimSpace(hardware) == "" {
		return errors.New("hardware is required")
	}
	if runs <= 0 {
		return errors.New("runs must be positive")
	}
	if timeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	return nil
}

func loadCorpus(path string) ([]evalharness.Scenario, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return evalharness.LoadCorpus(f)
}

func canonicalToolDefinitions() ([]evalharness.ToolDefinition, string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "companion-free-tier-tools-*")
	if err != nil {
		return nil, "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	data, err := store.Open(filepath.Join(tmpDir, "tools.db"))
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	cleanup = func() {
		_ = data.Close()
		_ = os.RemoveAll(tmpDir)
	}
	registry := capability.NewToolRegistry()
	if err := tools.RegisterNative(registry, tools.NativeDependencies{
		Store: data, RecordingsDir: filepath.Join(tmpDir, "recordings"), Now: func() time.Time {
			return time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC)
		},
	}); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	mem := memory.New(data, memory.HashEmbedding{Dimensions: 32})
	if err := tools.RegisterPlatform(registry, tools.PlatformDependencies{Memory: mem, Now: func() time.Time {
		return time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC)
	}}); err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	canonical := registry.DefinitionsForPacks(benchmarkPacks)
	definitions := make([]evalharness.ToolDefinition, 0, len(canonical))
	for _, definition := range canonical {
		definitions = append(definitions, evalharness.ToolDefinition{
			Type: "function",
			Function: evalharness.ToolFunction{
				Name: definition.Name, Description: definition.Description, Parameters: definition.Parameters,
			},
		})
	}
	raw, err := json.Marshal(definitions)
	if err != nil {
		cleanup()
		return nil, "", func() {}, err
	}
	sum := sha256.Sum256(raw)
	return definitions, hex.EncodeToString(sum[:]), cleanup, nil
}

func hydrateAndValidateCorpus(corpus []evalharness.Scenario, definitions []evalharness.ToolDefinition) error {
	if len(corpus) == 0 {
		return errors.New("tool-action corpus is empty")
	}
	known := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		known[definition.Function.Name] = struct{}{}
	}
	languages := map[string]bool{}
	for i := range corpus {
		if corpus[i].Kind != "tool_action" {
			return fmt.Errorf("scenario %s has unexpected kind %q", corpus[i].ID, corpus[i].Kind)
		}
		languages[strings.ToLower(strings.TrimSpace(corpus[i].Language))] = true
		corpus[i].Tools = cloneDefinitions(definitions)
		for _, call := range corpus[i].Expect.ToolCalls {
			if _, ok := known[call.Name]; !ok {
				return fmt.Errorf("scenario %s expects unknown canonical tool %q", corpus[i].ID, call.Name)
			}
		}
		for _, name := range corpus[i].Expect.ForbiddenTools {
			if _, ok := known[name]; !ok {
				return fmt.Errorf("scenario %s forbids unknown canonical tool %q", corpus[i].ID, name)
			}
		}
	}
	for _, language := range []string{"vi", "en", "mixed"} {
		if !languages[language] {
			return fmt.Errorf("tool-action corpus is missing %s coverage", language)
		}
	}
	return nil
}

func cloneDefinitions(in []evalharness.ToolDefinition) []evalharness.ToolDefinition {
	out := make([]evalharness.ToolDefinition, len(in))
	copy(out, in)
	return out
}

func strictEvidenceGate(report evalharness.Report) error {
	if report.EvidenceClass != evalharness.EvidenceClassProviderMeasured {
		return fmt.Errorf("unexpected evidence class %q", report.EvidenceClass)
	}
	if report.Summary.Failures != 0 {
		return fmt.Errorf("provider failures=%d", report.Summary.Failures)
	}
	if report.Summary.Quality.ForbiddenToolCalls != 0 {
		return fmt.Errorf("forbidden tool calls=%d", report.Summary.Quality.ForbiddenToolCalls)
	}
	failed := 0
	for _, trial := range report.Trials {
		if trial.Metrics.Final.TaskSuccess == nil || !*trial.Metrics.Final.TaskSuccess {
			failed++
		}
	}
	if failed != 0 {
		return fmt.Errorf("task failures=%d/%d", failed, len(report.Trials))
	}
	return nil
}

func writeReport(path string, stdout io.Writer, report evalharness.Report) error {
	var writer io.Writer = stdout
	var file *os.File
	if path != "-" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
			return err
		}
		var err error
		file, err = os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
