package main

import (
	"context"
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
	"companion-server/internal/contextengine"
	"companion-server/internal/memory"
	"companion-server/internal/providers/resources"
	"companion-server/internal/providers/tools"
	"companion-server/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("companion-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var inputPrice, outputPrice optionalFloat
	var seed optionalInt64
	options := cliOptions{}
	flags.StringVar(&options.scenarios, "scenarios", "./eval/scenarios.jsonl", "scenario JSONL path, or - for stdin")
	flags.StringVar(&options.output, "out", "-", "report JSON path, or - for stdout")
	flags.StringVar(&options.mode, "mode", "mock", "provider mode: mock, openai, or personal_retrieval")
	flags.IntVar(&options.runs, "runs", 1, "ordered repetitions of every scenario")
	flags.DurationVar(&options.requestTimeout, "request-timeout", 2*time.Minute, "timeout for each provider request")
	flags.StringVar(&options.providerName, "provider", "", "primary provider label")
	flags.StringVar(&options.endpoint, "endpoint", "", "OpenAI-compatible base URL or chat-completions URL")
	flags.StringVar(&options.model, "model", "", "primary model identifier")
	flags.StringVar(&options.version, "model-version", "", "primary model artifact/version")
	flags.StringVar(&options.quantization, "quantization", "", "primary quantization")
	flags.StringVar(&options.runtime, "runtime", "", "primary runtime name/version")
	flags.StringVar(&options.region, "region", "", "provider region, or local for an on-device/on-host runtime")
	flags.StringVar(&options.apiKeyEnv, "api-key-env", "OPENAI_API_KEY", "environment variable containing the primary API key")
	flags.BoolVar(&options.stream, "stream", true, "request streaming responses and measure TTFT")
	flags.Var(&seed, "seed", "optional provider seed")
	flags.IntVar(&options.maxTokens, "max-tokens", 512, "maximum completion tokens")
	flags.BoolVar(&options.mockEscalation, "mock-escalation", true, "run mock escalation fixtures when requested")
	flags.StringVar(&options.escalationProvider, "escalation-provider", "", "escalation provider label")
	flags.StringVar(&options.escalationEndpoint, "escalation-endpoint", "", "escalation OpenAI-compatible endpoint")
	flags.StringVar(&options.escalationModel, "escalation-model", "", "escalation model identifier")
	flags.StringVar(&options.escalationVersion, "escalation-model-version", "", "escalation model artifact/version")
	flags.StringVar(&options.escalationQuantization, "escalation-quantization", "", "escalation quantization")
	flags.StringVar(&options.escalationRuntime, "escalation-runtime", "", "escalation runtime name/version")
	flags.StringVar(&options.escalationRegion, "escalation-region", "", "escalation provider region, or local")
	flags.StringVar(&options.escalationAPIKeyEnv, "escalation-api-key-env", "OPENAI_API_KEY", "environment variable containing the escalation API key")
	flags.Var(&inputPrice, "input-usd-per-million", "optional input-token price used only for estimated cost")
	flags.Var(&outputPrice, "output-usd-per-million", "optional output-token price used only for estimated cost")
	flags.StringVar(&options.runID, "run-id", "", "caller-supplied stable run identifier")
	flags.StringVar(&options.hardware, "hardware", "", "measured host hardware description")
	flags.StringVar(&options.runtimeConfig, "runtime-config", "", "runtime configuration/artifact reference")
	flags.StringVar(&options.promptVersion, "prompt-version", "", "prompt version or digest")
	flags.StringVar(&options.toolSchemaCommit, "tool-schema-commit", "", "tool-schema source commit")
	flags.StringVar(&options.sourceCommit, "source-commit", "", "benchmark source commit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	options.seed = seed.pointer()
	options.inputPrice = inputPrice.pointer()
	options.outputPrice = outputPrice.pointer()
	if err := options.validate(); err != nil {
		fmt.Fprintf(stderr, "configuration: %v\n", err)
		return 2
	}

	if options.mode == "personal_retrieval" {
		reader, closeInput, err := openInput(options.scenarios)
		if err != nil {
			fmt.Fprintf(stderr, "open scenarios: %v\n", err)
			return 2
		}
		defer closeInput()
		scenarios, _, err := evalharness.LoadPersonalRetrievalCorpus(reader)
		if err != nil {
			fmt.Fprintf(stderr, "load retrieval scenarios: %v\n", err)
			return 2
		}

		tmpDir, err := os.MkdirTemp("", "eval-retrieval-*")
		if err != nil {
			fmt.Fprintf(stderr, "create temp dir: %v\n", err)
			return 1
		}
		defer os.RemoveAll(tmpDir)

		data, err := store.Open(filepath.Join(tmpDir, "retrieval.db"))
		if err != nil {
			fmt.Fprintf(stderr, "open store: %v\n", err)
			return 1
		}
		defer data.Close()

		baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
		mem := memory.New(data, memory.HashEmbedding{Dimensions: 32})
		toolRegistry := capability.NewToolRegistry()
		_ = tools.RegisterNative(toolRegistry, tools.NativeDependencies{
			Store:         data,
			RecordingsDir: filepath.Join(tmpDir, "recordings"),
			Now:           func() time.Time { return baseTime },
		})
		_ = tools.RegisterPlatform(toolRegistry, tools.PlatformDependencies{
			Memory: mem,
			Now:    func() time.Time { return baseTime },
		})
		resourceRegistry := capability.NewResourceRegistry()
		_ = resourceRegistry.Register(resources.NewNative(data, nil, time.UTC))
		router := contextengine.New(resourceRegistry)

		report, err := evalharness.RunPersonalRetrievalEvaluation(context.Background(), scenarios, evalharness.RetrievalDependencies{
			Store:         data,
			Memory:        mem,
			Registry:      toolRegistry,
			Router:        router,
			Resources:     resourceRegistry,
			RecordingsDir: filepath.Join(tmpDir, "recordings"),
			Now:           func() time.Time { return baseTime },
		})
		if err != nil {
			fmt.Fprintf(stderr, "run personal retrieval evaluation: %v\n", err)
			return 1
		}
		if err := writeJSONReport(options.output, stdout, report); err != nil {
			fmt.Fprintf(stderr, "write report: %v\n", err)
			return 1
		}
		if report.FailedCases > 0 {
			fmt.Fprintf(stderr, "personal retrieval benchmark failed: %d/%d passed (%.2f)\n", report.PassedCases, report.TotalCases, report.PassRate)
			return 1
		}
		return 0
	}

	reader, closeInput, err := openInput(options.scenarios)
	if err != nil {
		fmt.Fprintf(stderr, "open scenarios: %v\n", err)
		return 2
	}
	defer closeInput()
	scenarios, corpusHash, err := evalharness.LoadCorpus(reader)
	if err != nil {
		fmt.Fprintf(stderr, "load scenarios: %v\n", err)
		return 2
	}
	primary, escalation, err := options.providers()
	if err != nil {
		fmt.Fprintf(stderr, "configure providers: %v\n", err)
		return 2
	}
	report, err := evalharness.Run(context.Background(), scenarios, evalharness.RunnerConfig{
		Runs:         options.runs,
		Primary:      primary,
		Escalation:   escalation,
		Pricing:      evalharness.Pricing{InputUSDPerMillion: options.inputPrice, OutputUSDPerMillion: options.outputPrice},
		CorpusSource: options.scenarios,
		CorpusSHA256: corpusHash,
		Metadata: evalharness.RunMetadata{
			RunID:            options.runID,
			Hardware:         options.hardware,
			RuntimeConfig:    options.runtimeConfig,
			PromptVersion:    options.promptVersion,
			ToolSchemaCommit: options.toolSchemaCommit,
			SourceCommit:     options.sourceCommit,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "run benchmark: %v\n", err)
		return 1
	}
	if err := writeJSONReport(options.output, stdout, report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if report.Summary.Failures > 0 || report.Summary.Escalation.Unavailable > 0 {
		fmt.Fprintf(stderr, "benchmark recorded %d provider failure(s) and %d unavailable escalation(s); inspect the report\n", report.Summary.Failures, report.Summary.Escalation.Unavailable)
		return 1
	}
	return 0
}

type cliOptions struct {
	scenarios, output, mode                       string
	runs                                          int
	requestTimeout                                time.Duration
	providerName, endpoint, model, version        string
	quantization, runtime, region, apiKeyEnv      string
	stream                                        bool
	seed                                          *int64
	maxTokens                                     int
	mockEscalation                                bool
	escalationProvider, escalationEndpoint        string
	escalationModel, escalationVersion            string
	escalationQuantization, escalationRuntime     string
	escalationRegion                              string
	escalationAPIKeyEnv                           string
	inputPrice, outputPrice                       *float64
	runID, hardware, runtimeConfig                string
	promptVersion, toolSchemaCommit, sourceCommit string
}

func (o cliOptions) validate() error {
	if o.runs <= 0 {
		return errors.New("runs must be positive")
	}
	if o.requestTimeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	if o.maxTokens <= 0 {
		return errors.New("max-tokens must be positive")
	}
	switch o.mode {
	case "mock", "personal_retrieval":
	case "openai":
		if strings.TrimSpace(o.endpoint) == "" || strings.TrimSpace(o.model) == "" {
			return errors.New("openai mode requires -endpoint and -model")
		}
		required := []struct{ name, value string }{
			{"provider", o.providerName}, {"model-version", o.version},
			{"runtime", o.runtime}, {"region", o.region}, {"run-id", o.runID},
			{"hardware", o.hardware}, {"runtime-config", o.runtimeConfig},
			{"prompt-version", o.promptVersion}, {"tool-schema-commit", o.toolSchemaCommit},
			{"source-commit", o.sourceCommit},
		}
		for _, field := range required {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("openai measurement requires -%s provenance", field.name)
			}
		}
		if (o.escalationEndpoint == "") != (o.escalationModel == "") {
			return errors.New("escalation-endpoint and escalation-model must be supplied together")
		}
		if o.escalationEndpoint != "" {
			escalationRequired := []struct{ name, value string }{
				{"escalation-provider", o.escalationProvider},
				{"escalation-model-version", o.escalationVersion},
				{"escalation-runtime", o.escalationRuntime},
				{"escalation-region", o.escalationRegion},
			}
			for _, field := range escalationRequired {
				if strings.TrimSpace(field.value) == "" {
					return fmt.Errorf("openai escalation measurement requires -%s provenance", field.name)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported mode %q", o.mode)
	}
	return nil
}

func (o cliOptions) providers() (evalharness.Provider, evalharness.Provider, error) {
	primaryMetadata := evalharness.ProviderMetadata{
		Name:         o.providerName,
		Model:        o.model,
		Version:      o.version,
		Quantization: o.quantization,
		Runtime:      o.runtime,
		Region:       o.region,
	}
	if o.mode == "mock" {
		primary := evalharness.NewMockProviderWithMetadata(primaryMetadata)
		if !o.mockEscalation {
			return primary, nil, nil
		}
		escalationMetadata := evalharness.ProviderMetadata{
			Name:         o.escalationProvider,
			Model:        o.escalationModel,
			Version:      o.escalationVersion,
			Quantization: o.escalationQuantization,
			Runtime:      o.escalationRuntime,
			Region:       o.escalationRegion,
		}
		return primary, evalharness.NewMockProviderWithMetadata(escalationMetadata), nil
	}
	client := &http.Client{Timeout: o.requestTimeout}
	primary, err := evalharness.NewOpenAIProvider(evalharness.OpenAIConfig{
		Name: o.providerName, Model: o.model, Version: o.version, Quantization: o.quantization,
		Runtime: o.runtime, Endpoint: o.endpoint, APIKey: os.Getenv(o.apiKeyEnv), Stream: o.stream,
		Region: o.region,
		Seed:   o.seed, MaxTokens: o.maxTokens, HTTPClient: client,
	})
	if err != nil {
		return nil, nil, err
	}
	if o.escalationEndpoint == "" {
		return primary, nil, nil
	}
	escalation, err := evalharness.NewOpenAIProvider(evalharness.OpenAIConfig{
		Name: o.escalationProvider, Model: o.escalationModel, Version: o.escalationVersion,
		Quantization: o.escalationQuantization, Runtime: o.escalationRuntime,
		Region:   o.escalationRegion,
		Endpoint: o.escalationEndpoint, APIKey: os.Getenv(o.escalationAPIKeyEnv), Stream: o.stream,
		Seed: o.seed, MaxTokens: o.maxTokens, HTTPClient: client,
	})
	if err != nil {
		return nil, nil, err
	}
	return primary, escalation, nil
}

type optionalFloat struct {
	set   bool
	value float64
}

func (v *optionalFloat) String() string {
	if !v.set {
		return ""
	}
	return fmt.Sprintf("%g", v.value)
}

func (v *optionalFloat) Set(raw string) error {
	if _, err := fmt.Sscan(raw, &v.value); err != nil {
		return err
	}
	v.set = true
	return nil
}

func (v optionalFloat) pointer() *float64 {
	if !v.set {
		return nil
	}
	value := v.value
	return &value
}

type optionalInt64 struct {
	set   bool
	value int64
}

func (v *optionalInt64) String() string {
	if !v.set {
		return ""
	}
	return fmt.Sprintf("%d", v.value)
}

func (v *optionalInt64) Set(raw string) error {
	if _, err := fmt.Sscan(raw, &v.value); err != nil {
		return err
	}
	v.set = true
	return nil
}

func (v optionalInt64) pointer() *int64 {
	if !v.set {
		return nil
	}
	value := v.value
	return &value
}

func openInput(path string) (io.Reader, func(), error) {
	if path == "-" {
		return os.Stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, func() {}, err
	}
	return file, func() { _ = file.Close() }, nil
}

func writeJSONReport(path string, stdout io.Writer, report any) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, err = stdout.Write(data)
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".companion-eval-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
