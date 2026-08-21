package speechbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = "companion.voice.provider-evidence.v1"

type CorpusManifest struct {
	Dataset  string       `json:"dataset"`
	Revision string       `json:"revision"`
	License  string       `json:"license"`
	Split    string       `json:"split"`
	Cases    []CorpusCase `json:"cases"`
}

type CorpusCase struct {
	ID             string   `json:"id"`
	Language       string   `json:"language"`
	Reference      string   `json:"reference"`
	PCMPath        string   `json:"pcm_path"`
	PCM_SHA256     string   `json:"pcm_sha256"`
	DurationMS     float64  `json:"duration_ms"`
	SourceRow      int      `json:"source_row"`
	SourceRows     []int    `json:"source_rows,omitempty"`
	SourceKind     string   `json:"source_kind"`
	ASRLanguage    string   `json:"asr_language,omitempty"`
	TTSVoice       string   `json:"tts_voice,omitempty"`
	TTSResponse    string   `json:"tts_response"`
}

type Report struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	SourceCommit  string         `json:"source_commit"`
	Runner        string         `json:"runner"`
	Corpus        CorpusSummary  `json:"corpus"`
	Lanes         []LaneResult   `json:"lanes"`
	Limitations   []string       `json:"limitations,omitempty"`
}

type CorpusSummary struct {
	Dataset      string `json:"dataset"`
	Revision     string `json:"revision"`
	License      string `json:"license"`
	Split        string `json:"split"`
	ManifestHash string `json:"manifest_sha256,omitempty"`
	CaseCount    int    `json:"case_count"`
}

type ProviderProvenance struct {
	Provider      string            `json:"provider"`
	Model         string            `json:"model,omitempty"`
	Voice         string            `json:"voice,omitempty"`
	Region        string            `json:"region,omitempty"`
	EndpointClass string            `json:"endpoint_class,omitempty"`
	Config        map[string]string `json:"config,omitempty"`
	QuotaNote     string            `json:"quota_note,omitempty"`
	PricingNote   string            `json:"pricing_note,omitempty"`
	PrivacyNote   string            `json:"privacy_note,omitempty"`
}

type LaneResult struct {
	Lane          string             `json:"lane"`
	Status        string             `json:"status"`
	EvidenceClass string             `json:"evidence_class"`
	Provenance    []ProviderProvenance `json:"provenance,omitempty"`
	Cases         []CaseResult       `json:"cases,omitempty"`
	Summary       *LaneSummary       `json:"summary,omitempty"`
	Cancellation  *CancellationResult `json:"cancellation,omitempty"`
	Reconnect     *ReconnectResult   `json:"reconnect,omitempty"`
	Blockers      []string           `json:"blockers,omitempty"`
	Failures      []string           `json:"failures,omitempty"`
}

type CaseResult struct {
	ID                   string  `json:"id"`
	Language             string  `json:"language"`
	Reference            string  `json:"reference"`
	Transcript           string  `json:"transcript,omitempty"`
	WER                   float64 `json:"wer,omitempty"`
	CER                   float64 `json:"cer,omitempty"`
	ASRFirstPartialMS     *float64 `json:"asr_first_partial_ms,omitempty"`
	ASRFinalMS            float64 `json:"asr_final_ms,omitempty"`
	TTSFirstAudioMS       float64 `json:"tts_first_audio_ms,omitempty"`
	TTSTotalMS            float64 `json:"tts_total_ms,omitempty"`
	TTSPCMBytes           int     `json:"tts_pcm_bytes,omitempty"`
	TTSChunks             int     `json:"tts_chunks,omitempty"`
	TurnE2EMS             float64 `json:"turn_e2e_ms,omitempty"`
	Error                 string  `json:"error,omitempty"`
}

type LaneSummary struct {
	CasesPassed       int     `json:"cases_passed"`
	CasesFailed       int     `json:"cases_failed"`
	WERMean           float64 `json:"wer_mean,omitempty"`
	CERMean           float64 `json:"cer_mean,omitempty"`
	ASRFinalP50MS     float64 `json:"asr_final_p50_ms,omitempty"`
	ASRFinalP95MS     float64 `json:"asr_final_p95_ms,omitempty"`
	TTSFirstAudioP50MS float64 `json:"tts_first_audio_p50_ms,omitempty"`
	TTSFirstAudioP95MS float64 `json:"tts_first_audio_p95_ms,omitempty"`
	TurnE2EP50MS      float64 `json:"turn_e2e_p50_ms,omitempty"`
	TurnE2EP95MS      float64 `json:"turn_e2e_p95_ms,omitempty"`
}

type CancellationResult struct {
	Attempted          bool    `json:"attempted"`
	Cancelled          bool    `json:"cancelled"`
	CancelLatencyMS    float64 `json:"cancel_latency_ms,omitempty"`
	StaleAudioChunks   int     `json:"stale_audio_chunks_after_cancel,omitempty"`
	Evidence           string  `json:"evidence,omitempty"`
	Error              string  `json:"error,omitempty"`
}

type ReconnectResult struct {
	Attempted bool   `json:"attempted"`
	Passed    bool   `json:"passed"`
	Evidence  string `json:"evidence,omitempty"`
	Error     string `json:"error,omitempty"`
}

func LoadCorpus(path string) (CorpusManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil { return CorpusManifest{}, err }
	var manifest CorpusManifest
	if err := json.Unmarshal(raw, &manifest); err != nil { return CorpusManifest{}, err }
	if strings.TrimSpace(manifest.Dataset) == "" || strings.TrimSpace(manifest.Revision) == "" || strings.TrimSpace(manifest.License) == "" { return CorpusManifest{}, errors.New("corpus manifest requires dataset, revision and license") }
	if len(manifest.Cases) == 0 { return CorpusManifest{}, errors.New("corpus manifest contains no cases") }
	seen := map[string]bool{}
	langs := map[string]bool{}
	for _, c := range manifest.Cases {
		if strings.TrimSpace(c.ID)=="" || strings.TrimSpace(c.Reference)=="" || strings.TrimSpace(c.PCMPath)=="" { return CorpusManifest{}, fmt.Errorf("invalid corpus case %#v", c) }
		if seen[c.ID] { return CorpusManifest{}, fmt.Errorf("duplicate corpus case %q", c.ID) }
		seen[c.ID]=true; langs[c.Language]=true
		if c.DurationMS <= 0 { return CorpusManifest{}, fmt.Errorf("corpus case %q has invalid duration", c.ID) }
		if strings.TrimSpace(c.PCM_SHA256)=="" { return CorpusManifest{}, fmt.Errorf("corpus case %q missing PCM hash", c.ID) }
	}
	for _, lang := range []string{"vi","en","mixed"} { if !langs[lang] { return CorpusManifest{}, fmt.Errorf("corpus missing required %s case", lang) } }
	return manifest, nil
}

func NewReport(commit, runner, manifestHash string, corpus CorpusManifest) Report {
	return Report{SchemaVersion:SchemaVersion, GeneratedAt:time.Now().UTC().Format(time.RFC3339), SourceCommit:commit, Runner:runner, Corpus:CorpusSummary{Dataset:corpus.Dataset,Revision:corpus.Revision,License:corpus.License,Split:corpus.Split,ManifestHash:manifestHash,CaseCount:len(corpus.Cases)}}
}

func Summarize(cases []CaseResult) *LaneSummary {
	if len(cases)==0 { return nil }
	var passed int; var wer,cer,asr,tts,turn []float64
	for _, c := range cases {
		if c.Error!="" { continue }
		passed++; wer=append(wer,c.WER); cer=append(cer,c.CER); asr=append(asr,c.ASRFinalMS); tts=append(tts,c.TTSFirstAudioMS); turn=append(turn,c.TurnE2EMS)
	}
	if passed==0 { return &LaneSummary{CasesFailed:len(cases)} }
	return &LaneSummary{CasesPassed:passed,CasesFailed:len(cases)-passed,WERMean:mean(wer),CERMean:mean(cer),ASRFinalP50MS:Percentile(asr,.50),ASRFinalP95MS:Percentile(asr,.95),TTSFirstAudioP50MS:Percentile(tts,.50),TTSFirstAudioP95MS:Percentile(tts,.95),TurnE2EP50MS:Percentile(turn,.50),TurnE2EP95MS:Percentile(turn,.95)}
}

func mean(values []float64) float64 { if len(values)==0{return 0}; var sum float64; for _,v:=range values{sum+=v}; return sum/float64(len(values)) }

func Percentile(values []float64, q float64) float64 {
	if len(values)==0{return 0}; sorted:=append([]float64(nil),values...); sort.Float64s(sorted); if q<=0{return sorted[0]}; if q>=1{return sorted[len(sorted)-1]}; pos:=q*float64(len(sorted)-1); lo:=int(pos); hi:=lo+1; if hi>=len(sorted){return sorted[lo]}; frac:=pos-float64(lo); return sorted[lo]*(1-frac)+sorted[hi]*frac
}
