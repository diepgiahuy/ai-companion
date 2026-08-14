// Package eval provides a reproducible, provider-neutral benchmark harness for
// Companion model candidates. It deliberately reports evidence, not promotion
// verdicts.
package eval

import "encoding/json"

const ReportSchemaVersion = "companion.eval.report.v1"

const (
	EvidenceClassSynthetic        = "synthetic"
	EvidenceClassProviderMeasured = "provider_measured"
	EvidenceClassMixed            = "mixed"
)

// Scenario is one JSONL benchmark case. The top-level must_pack, fallback, and
// exact fields preserve the original routing corpus format.
type Scenario struct {
	ID       string           `json:"id,omitempty"`
	Kind     string           `json:"kind,omitempty"`
	Input    string           `json:"input"`
	Language string           `json:"language,omitempty"`
	System   string           `json:"system,omitempty"`
	History  []Message        `json:"history,omitempty"`
	Tools    []ToolDefinition `json:"tools,omitempty"`

	MustPack []string `json:"must_pack,omitempty"`
	Fallback bool     `json:"fallback,omitempty"`
	Exact    bool     `json:"exact,omitempty"`

	Expect Expectations `json:"expect,omitempty"`
	Mock   *MockCase    `json:"mock,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolDefinition follows the function-tool shape accepted by OpenAI-compatible
// chat-completions endpoints.
type ToolDefinition struct {
	Type     string       `json:"type,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type Expectations struct {
	Packs          []string           `json:"packs,omitempty"`
	ExactPacks     bool               `json:"exact_packs,omitempty"`
	ToolCalls      []ExpectedToolCall `json:"tool_calls,omitempty"`
	NoToolCall     bool               `json:"no_tool_call,omitempty"`
	ForbiddenTools []string           `json:"forbidden_tools,omitempty"`
	OutputExact    string             `json:"output_exact,omitempty"`
	MustContain    []string           `json:"must_contain,omitempty"`
	MustNotContain []string           `json:"must_not_contain,omitempty"`
	RetrievalIDs   []string           `json:"retrieval_ids,omitempty"`
	RetrievalK     int                `json:"retrieval_k,omitempty"`
	Escalate       *bool              `json:"escalate,omitempty"`
}

type ExpectedToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type MockCase struct {
	Primary    MockResponse  `json:"primary"`
	Escalation *MockResponse `json:"escalation,omitempty"`
}

type MockResponse struct {
	Observation Observation `json:"observation"`
	TTFTUS      *int64      `json:"ttft_us,omitempty"`
	TotalUS     int64       `json:"total_us,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// Observation is the normalized output scored by the harness. Usage is absent
// unless the provider actually returned it or a mock fixture explicitly set it.
type Observation struct {
	Text         string     `json:"text,omitempty"`
	Packs        []string   `json:"packs,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	RetrievedIDs []string   `json:"retrieved_ids,omitempty"`
	Escalate     *bool      `json:"escalate,omitempty"`
	Usage        *Usage     `json:"usage,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type Timing struct {
	TTFTUS  *int64 `json:"ttft_us,omitempty"`
	TotalUS int64  `json:"total_us"`
}

type ProviderMetadata struct {
	Name         string `json:"name"`
	Model        string `json:"model,omitempty"`
	Version      string `json:"version,omitempty"`
	Quantization string `json:"quantization,omitempty"`
	Runtime      string `json:"runtime,omitempty"`
	Region       string `json:"region,omitempty"`
	Endpoint     string `json:"endpoint,omitempty"`
}

type RunMetadata struct {
	RunID            string `json:"run_id,omitempty"`
	Hardware         string `json:"hardware,omitempty"`
	RuntimeConfig    string `json:"runtime_config,omitempty"`
	PromptVersion    string `json:"prompt_version,omitempty"`
	ToolSchemaCommit string `json:"tool_schema_commit,omitempty"`
	SourceCommit     string `json:"source_commit,omitempty"`
}

type Pricing struct {
	InputUSDPerMillion  *float64 `json:"input_usd_per_million,omitempty"`
	OutputUSDPerMillion *float64 `json:"output_usd_per_million,omitempty"`
}

type Report struct {
	SchemaVersion string              `json:"schema_version"`
	EvidenceClass string              `json:"evidence_class"`
	Selection     string              `json:"selection"`
	Corpus        CorpusMetadata      `json:"corpus"`
	Configuration ReportConfiguration `json:"configuration"`
	Trials        []TrialResult       `json:"trials"`
	Summary       Summary             `json:"summary"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type CorpusMetadata struct {
	Source        string `json:"source"`
	SHA256        string `json:"sha256"`
	ScenarioCount int    `json:"scenario_count"`
}

type ReportConfiguration struct {
	Runs       int               `json:"runs"`
	Primary    ProviderMetadata  `json:"primary"`
	Escalation *ProviderMetadata `json:"escalation,omitempty"`
	Pricing    Pricing           `json:"pricing"`
	Metadata   RunMetadata       `json:"metadata"`
}

type TrialResult struct {
	ScenarioID    string       `json:"scenario_id"`
	Kind          string       `json:"kind,omitempty"`
	Run           int          `json:"run"`
	Primary       StageResult  `json:"primary"`
	Escalation    *StageResult `json:"escalation,omitempty"`
	FinalStage    string       `json:"final_stage"`
	OverallTiming Timing       `json:"overall_timing"`
	Metrics       TrialMetrics `json:"metrics"`
}

type StageResult struct {
	Observation Observation `json:"observation"`
	Timing      Timing      `json:"timing"`
	CostUSD     *float64    `json:"estimated_cost_usd,omitempty"`
	Error       string      `json:"error,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
}

type TrialMetrics struct {
	Primary             QualityMetrics `json:"primary"`
	Final               QualityMetrics `json:"final"`
	QualityDelta        *float64       `json:"quality_delta,omitempty"`
	EscalationRequested bool           `json:"escalation_requested"`
	EscalationExpected  *bool          `json:"escalation_expected,omitempty"`
}

type QualityMetrics struct {
	PackSelection      *SetMetrics       `json:"pack_selection,omitempty"`
	ToolSelection      *SetMetrics       `json:"tool_selection,omitempty"`
	ArgumentMatch      *RateMetric       `json:"argument_match,omitempty"`
	SchemaValidity     *RateMetric       `json:"schema_validity,omitempty"`
	ContentChecks      *RateMetric       `json:"content_checks,omitempty"`
	Retrieval          *RetrievalMetrics `json:"retrieval,omitempty"`
	EscalationCorrect  *bool             `json:"escalation_correct,omitempty"`
	ForbiddenToolCalls int               `json:"forbidden_tool_calls"`
	DeterministicScore *float64          `json:"deterministic_score,omitempty"`
	TaskSuccess        *bool             `json:"task_success,omitempty"`
}

type SetMetrics struct {
	Expected      []string `json:"expected"`
	Observed      []string `json:"observed"`
	TruePositive  int      `json:"true_positive"`
	FalsePositive int      `json:"false_positive"`
	FalseNegative int      `json:"false_negative"`
	Precision     float64  `json:"precision"`
	Recall        float64  `json:"recall"`
	F1            float64  `json:"f1"`
	Exact         bool     `json:"exact"`
}

type RateMetric struct {
	Correct   int     `json:"correct"`
	Evaluated int     `json:"evaluated"`
	Rate      float64 `json:"rate"`
}

type RetrievalMetrics struct {
	Expected  []string `json:"expected"`
	Observed  []string `json:"observed"`
	K         int      `json:"k"`
	RecallAtK float64  `json:"recall_at_k"`
	NDCG      float64  `json:"ndcg"`
}

type Summary struct {
	Trials     int               `json:"trials"`
	Failures   int               `json:"provider_failures"`
	Latency    LatencySummary    `json:"latency"`
	Quality    QualitySummary    `json:"quality"`
	Cost       CostSummary       `json:"cost"`
	Escalation EscalationSummary `json:"escalation"`
}

type LatencySummary struct {
	TotalSamples int    `json:"total_samples"`
	TTFTSamples  int    `json:"ttft_samples"`
	P50TTFTUS    *int64 `json:"p50_ttft_us,omitempty"`
	P95TTFTUS    *int64 `json:"p95_ttft_us,omitempty"`
	P50TotalUS   *int64 `json:"p50_total_us,omitempty"`
	P95TotalUS   *int64 `json:"p95_total_us,omitempty"`
}

type QualitySummary struct {
	ScoredTrials       int      `json:"scored_trials"`
	MeanScore          *float64 `json:"mean_deterministic_score,omitempty"`
	TaskSuccessTrials  int      `json:"task_success_trials"`
	TaskSuccessRate    *float64 `json:"task_success_rate,omitempty"`
	ForbiddenToolCalls int      `json:"forbidden_tool_calls"`
}

type CostSummary struct {
	UsageSamples      int      `json:"usage_samples"`
	PricedSamples     int      `json:"priced_samples"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	TotalTokens       int64    `json:"total_tokens"`
	EstimatedUSD      *float64 `json:"estimated_usd,omitempty"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type EscalationSummary struct {
	GroundTruthTrials       int      `json:"ground_truth_trials"`
	TruePositive            int      `json:"true_positive"`
	FalsePositive           int      `json:"false_positive"`
	TrueNegative            int      `json:"true_negative"`
	FalseNegative           int      `json:"false_negative"`
	Precision               *float64 `json:"precision,omitempty"`
	Recall                  *float64 `json:"recall,omitempty"`
	Requested               int      `json:"requested"`
	Executed                int      `json:"executed"`
	Unavailable             int      `json:"unavailable"`
	Failures                int      `json:"failures"`
	QualityDeltaSamples     int      `json:"quality_delta_samples"`
	MeanQualityDelta        *float64 `json:"mean_quality_delta,omitempty"`
	LatencyPenaltySamples   int      `json:"latency_penalty_samples"`
	P50LatencyPenaltyUS     *int64   `json:"p50_latency_penalty_us,omitempty"`
	P95LatencyPenaltyUS     *int64   `json:"p95_latency_penalty_us,omitempty"`
	EstimatedCostPenaltyUSD *float64 `json:"estimated_cost_penalty_usd,omitempty"`
}
