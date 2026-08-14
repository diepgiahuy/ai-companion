package eval

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

type RunnerConfig struct {
	Runs         int
	Primary      Provider
	Escalation   Provider
	Pricing      Pricing
	Metadata     RunMetadata
	CorpusSource string
	CorpusSHA256 string
}

func Run(ctx context.Context, scenarios []Scenario, cfg RunnerConfig) (Report, error) {
	if len(scenarios) == 0 {
		return Report{}, fmt.Errorf("at least one scenario is required")
	}
	if cfg.Runs <= 0 {
		return Report{}, fmt.Errorf("runs must be positive")
	}
	if cfg.Primary == nil {
		return Report{}, fmt.Errorf("primary provider is required")
	}
	if err := validateProviderClass(cfg.Primary); err != nil {
		return Report{}, fmt.Errorf("primary provider: %w", err)
	}
	if cfg.Escalation != nil {
		if err := validateProviderClass(cfg.Escalation); err != nil {
			return Report{}, fmt.Errorf("escalation provider: %w", err)
		}
	}
	if err := validatePricing(cfg.Pricing); err != nil {
		return Report{}, err
	}
	if err := validateMeasurementProvenance(cfg); err != nil {
		return Report{}, err
	}
	evidenceClass := cfg.Primary.EvidenceClass()
	var escalationMetadata *ProviderMetadata
	if cfg.Escalation != nil {
		metadata := cfg.Escalation.Metadata()
		escalationMetadata = &metadata
		if cfg.Escalation.EvidenceClass() != evidenceClass {
			evidenceClass = EvidenceClassMixed
		}
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion,
		EvidenceClass: evidenceClass,
		Selection:     "not_selected",
		Corpus: CorpusMetadata{
			Source:        cfg.CorpusSource,
			SHA256:        cfg.CorpusSHA256,
			ScenarioCount: len(scenarios),
		},
		Configuration: ReportConfiguration{
			Runs:       cfg.Runs,
			Primary:    cfg.Primary.Metadata(),
			Escalation: escalationMetadata,
			Pricing:    cfg.Pricing,
			Metadata:   cfg.Metadata,
		},
		Trials: make([]TrialResult, 0, len(scenarios)*cfg.Runs),
	}
	for run := 1; run <= cfg.Runs; run++ {
		for _, scenario := range scenarios {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			trial := runTrial(ctx, scenario, run, cfg)
			report.Trials = append(report.Trials, trial)
		}
	}
	report.Summary = summarize(report.Trials, cfg.Pricing)
	if report.EvidenceClass == EvidenceClassSynthetic {
		report.Warnings = append(report.Warnings, "synthetic mock evidence cannot establish real-provider quality, latency, cost, or production readiness")
	}
	return report, nil
}

func runTrial(ctx context.Context, scenario Scenario, run int, cfg RunnerConfig) TrialResult {
	primaryResponse, primaryErr := cfg.Primary.Evaluate(ctx, ProviderRequest{Scenario: scenario, Run: run, Stage: StagePrimary})
	if primaryErr == nil {
		primaryErr = validateProviderResponse(primaryResponse)
	}
	primary := stageResult(primaryResponse, primaryErr, cfg.Pricing)
	trial := TrialResult{
		ScenarioID:    scenario.ID,
		Kind:          scenario.Kind,
		Run:           run,
		Primary:       primary,
		FinalStage:    string(StagePrimary),
		OverallTiming: primary.Timing,
		Metrics: TrialMetrics{
			Primary:             Score(scenario, primary.Observation),
			EscalationExpected:  cloneBool(scenario.Expect.Escalate),
			EscalationRequested: primary.Observation.Escalate != nil && *primary.Observation.Escalate,
		},
	}
	taskScenario := scenario
	taskScenario.Expect.Escalate = nil
	primaryTaskMetrics := Score(taskScenario, primary.Observation)
	trial.Metrics.Final = primaryTaskMetrics
	if primaryErr != nil {
		markTaskFailed(&trial.Metrics.Primary)
		markTaskFailed(&trial.Metrics.Final)
		trial.Metrics.EscalationRequested = false
		return trial
	}
	if !trial.Metrics.EscalationRequested {
		return trial
	}
	if cfg.Escalation == nil {
		trial.Primary.Warnings = append(trial.Primary.Warnings, "escalation requested but no escalation provider was configured")
		markTaskFailed(&trial.Metrics.Final)
		return trial
	}
	escalationResponse, escalationErr := cfg.Escalation.Evaluate(ctx, ProviderRequest{Scenario: scenario, Run: run, Stage: StageEscalation})
	if escalationErr == nil {
		escalationErr = validateProviderResponse(escalationResponse)
	}
	escalation := stageResult(escalationResponse, escalationErr, cfg.Pricing)
	trial.Escalation = &escalation
	trial.FinalStage = string(StageEscalation)
	trial.Metrics.Final = Score(taskScenario, escalation.Observation)
	if escalationErr != nil {
		markTaskFailed(&trial.Metrics.Final)
	}
	trial.OverallTiming = combineTiming(primary.Timing, escalation.Timing)
	if primaryTaskMetrics.DeterministicScore != nil && trial.Metrics.Final.DeterministicScore != nil {
		delta := *trial.Metrics.Final.DeterministicScore - *primaryTaskMetrics.DeterministicScore
		trial.Metrics.QualityDelta = &delta
	}
	return trial
}

func validateProviderClass(provider Provider) error {
	switch provider.EvidenceClass() {
	case EvidenceClassSynthetic, EvidenceClassProviderMeasured:
		return nil
	default:
		return fmt.Errorf("unsupported evidence class %q", provider.EvidenceClass())
	}
}

func validateMeasurementProvenance(cfg RunnerConfig) error {
	requireProvider := func(label string, provider Provider) error {
		if provider == nil || provider.EvidenceClass() != EvidenceClassProviderMeasured {
			return nil
		}
		metadata := provider.Metadata()
		fields := []struct{ name, value string }{
			{"name", metadata.Name}, {"model", metadata.Model}, {"version", metadata.Version},
			{"runtime", metadata.Runtime}, {"region", metadata.Region}, {"endpoint", metadata.Endpoint},
		}
		for _, field := range fields {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("%s measured provider metadata requires %s", label, field.name)
			}
		}
		return nil
	}
	if err := requireProvider("primary", cfg.Primary); err != nil {
		return err
	}
	if err := requireProvider("escalation", cfg.Escalation); err != nil {
		return err
	}
	if cfg.Primary.EvidenceClass() != EvidenceClassProviderMeasured &&
		(cfg.Escalation == nil || cfg.Escalation.EvidenceClass() != EvidenceClassProviderMeasured) {
		return nil
	}
	fields := []struct{ name, value string }{
		{"run_id", cfg.Metadata.RunID}, {"hardware", cfg.Metadata.Hardware},
		{"runtime_config", cfg.Metadata.RuntimeConfig}, {"prompt_version", cfg.Metadata.PromptVersion},
		{"tool_schema_commit", cfg.Metadata.ToolSchemaCommit}, {"source_commit", cfg.Metadata.SourceCommit},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("provider measurement metadata requires %s", field.name)
		}
	}
	return nil
}

func validateProviderResponse(response ProviderResponse) error {
	if response.Timing.TotalUS < 0 || (response.Timing.TTFTUS != nil && (*response.Timing.TTFTUS < 0 || *response.Timing.TTFTUS > response.Timing.TotalUS)) {
		return fmt.Errorf("provider returned invalid timing")
	}
	usage := response.Observation.Usage
	if usage != nil {
		if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 {
			return fmt.Errorf("provider returned negative token usage")
		}
		if usage.TotalTokens > 0 && usage.TotalTokens < usage.InputTokens+usage.OutputTokens {
			return fmt.Errorf("provider returned inconsistent token usage")
		}
	}
	return nil
}

func markTaskFailed(metrics *QualityMetrics) {
	failed := false
	metrics.TaskSuccess = &failed
}

func stageResult(response ProviderResponse, err error, pricing Pricing) StageResult {
	result := StageResult{
		Observation: response.Observation,
		Timing:      response.Timing,
		Warnings:    append([]string(nil), response.Warnings...),
	}
	if err != nil {
		result.Error = err.Error()
	}
	result.CostUSD = estimateCost(response.Observation.Usage, pricing)
	return result
}

func combineTiming(primary, escalation Timing) Timing {
	total := primary.TotalUS + escalation.TotalUS
	var ttft *int64
	if escalation.TTFTUS != nil {
		value := primary.TotalUS + *escalation.TTFTUS
		ttft = &value
	}
	return Timing{TTFTUS: ttft, TotalUS: total}
}

func summarize(trials []TrialResult, pricing Pricing) Summary {
	summary := Summary{Trials: len(trials)}
	var totalLatencies, ttfts []int64
	var qualityScores, qualityDeltas []float64
	var successes int
	var escalationPenalties []int64
	var estimatedCost, escalationCost float64
	escalationPriced := false
	for _, trial := range trials {
		failed := trial.Primary.Error != "" || (trial.Escalation != nil && trial.Escalation.Error != "")
		if trial.Primary.Error != "" {
			summary.Failures++
		}
		if trial.Escalation != nil && trial.Escalation.Error != "" {
			summary.Failures++
		}
		if !failed {
			totalLatencies = append(totalLatencies, trial.OverallTiming.TotalUS)
			if trial.OverallTiming.TTFTUS != nil {
				ttfts = append(ttfts, *trial.OverallTiming.TTFTUS)
			}
		}
		if !failed {
			if trial.Metrics.Final.DeterministicScore != nil {
				qualityScores = append(qualityScores, *trial.Metrics.Final.DeterministicScore)
			}
			if trial.Metrics.Final.TaskSuccess != nil {
				summary.Quality.TaskSuccessTrials++
				if *trial.Metrics.Final.TaskSuccess {
					successes++
				}
			}
			summary.Quality.ForbiddenToolCalls += trial.Metrics.Final.ForbiddenToolCalls
		}
		if trial.Metrics.EscalationExpected != nil {
			summary.Escalation.GroundTruthTrials++
			expected, requested := *trial.Metrics.EscalationExpected, trial.Metrics.EscalationRequested
			switch {
			case expected && requested:
				summary.Escalation.TruePositive++
			case !expected && requested:
				summary.Escalation.FalsePositive++
			case !expected && !requested:
				summary.Escalation.TrueNegative++
			case expected && !requested:
				summary.Escalation.FalseNegative++
			}
		}
		if trial.Metrics.EscalationRequested {
			summary.Escalation.Requested++
			if trial.Escalation == nil {
				summary.Escalation.Unavailable++
			} else {
				summary.Escalation.Executed++
				escalationPenalties = append(escalationPenalties, trial.Escalation.Timing.TotalUS)
				if trial.Escalation.Error != "" {
					summary.Escalation.Failures++
				}
				if trial.Escalation.CostUSD != nil {
					escalationCost += *trial.Escalation.CostUSD
					escalationPriced = true
				}
			}
		}
		if trial.Metrics.QualityDelta != nil {
			qualityDeltas = append(qualityDeltas, *trial.Metrics.QualityDelta)
		}
		accumulateUsage(&summary.Cost, trial.Primary.Observation.Usage, trial.Primary.CostUSD)
		if trial.Primary.CostUSD != nil {
			estimatedCost += *trial.Primary.CostUSD
		}
		if trial.Escalation != nil {
			accumulateUsage(&summary.Cost, trial.Escalation.Observation.Usage, trial.Escalation.CostUSD)
			if trial.Escalation.CostUSD != nil {
				estimatedCost += *trial.Escalation.CostUSD
			}
		}
	}
	summary.Latency.TotalSamples = len(totalLatencies)
	summary.Latency.TTFTSamples = len(ttfts)
	summary.Latency.P50TotalUS = percentile(totalLatencies, 0.50)
	summary.Latency.P95TotalUS = percentile(totalLatencies, 0.95)
	summary.Latency.P50TTFTUS = percentile(ttfts, 0.50)
	summary.Latency.P95TTFTUS = percentile(ttfts, 0.95)
	summary.Quality.ScoredTrials = len(qualityScores)
	if len(qualityScores) > 0 {
		value := mean(qualityScores)
		summary.Quality.MeanScore = &value
	}
	if summary.Quality.TaskSuccessTrials > 0 {
		value := float64(successes) / float64(summary.Quality.TaskSuccessTrials)
		summary.Quality.TaskSuccessRate = &value
	}
	if summary.Cost.PricedSamples > 0 {
		summary.Cost.EstimatedUSD = &estimatedCost
	}
	if summary.Cost.UsageSamples == 0 {
		summary.Cost.UnavailableReason = "provider did not report token usage"
	} else if pricing.InputUSDPerMillion == nil || pricing.OutputUSDPerMillion == nil {
		summary.Cost.UnavailableReason = "pricing rates not supplied; USD cost not calculated"
	}
	setEscalationRates(&summary.Escalation)
	summary.Escalation.QualityDeltaSamples = len(qualityDeltas)
	if len(qualityDeltas) > 0 {
		value := mean(qualityDeltas)
		summary.Escalation.MeanQualityDelta = &value
	}
	summary.Escalation.LatencyPenaltySamples = len(escalationPenalties)
	summary.Escalation.P50LatencyPenaltyUS = percentile(escalationPenalties, 0.50)
	summary.Escalation.P95LatencyPenaltyUS = percentile(escalationPenalties, 0.95)
	if escalationPriced {
		summary.Escalation.EstimatedCostPenaltyUSD = &escalationCost
	}
	return summary
}

func setEscalationRates(summary *EscalationSummary) {
	precisionDenominator := summary.TruePositive + summary.FalsePositive
	if precisionDenominator > 0 {
		value := float64(summary.TruePositive) / float64(precisionDenominator)
		summary.Precision = &value
	}
	recallDenominator := summary.TruePositive + summary.FalseNegative
	if recallDenominator > 0 {
		value := float64(summary.TruePositive) / float64(recallDenominator)
		summary.Recall = &value
	}
}

func accumulateUsage(summary *CostSummary, usage *Usage, cost *float64) {
	if usage == nil {
		return
	}
	summary.UsageSamples++
	summary.InputTokens += usage.InputTokens
	summary.OutputTokens += usage.OutputTokens
	if usage.TotalTokens > 0 {
		summary.TotalTokens += usage.TotalTokens
	} else {
		summary.TotalTokens += usage.InputTokens + usage.OutputTokens
	}
	if cost != nil {
		summary.PricedSamples++
	}
}

func estimateCost(usage *Usage, pricing Pricing) *float64 {
	if usage == nil || pricing.InputUSDPerMillion == nil || pricing.OutputUSDPerMillion == nil {
		return nil
	}
	value := float64(usage.InputTokens)**pricing.InputUSDPerMillion/1_000_000 +
		float64(usage.OutputTokens)**pricing.OutputUSDPerMillion/1_000_000
	return &value
}

func validatePricing(pricing Pricing) error {
	if pricing.InputUSDPerMillion != nil && (*pricing.InputUSDPerMillion < 0 || math.IsNaN(*pricing.InputUSDPerMillion) || math.IsInf(*pricing.InputUSDPerMillion, 0)) {
		return fmt.Errorf("input token price must be finite and non-negative")
	}
	if pricing.OutputUSDPerMillion != nil && (*pricing.OutputUSDPerMillion < 0 || math.IsNaN(*pricing.OutputUSDPerMillion) || math.IsInf(*pricing.OutputUSDPerMillion, 0)) {
		return fmt.Errorf("output token price must be finite and non-negative")
	}
	return nil
}

func percentile(values []int64, quantile float64) *int64 {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]int64(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(quantile*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	value := ordered[index]
	return &value
}
