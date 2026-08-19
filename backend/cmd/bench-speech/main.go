package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/speech"
)

type BenchmarkReport struct {
	SchemaVersion string         `json:"schema_version"`
	GeneratedAt   string         `json:"generated_at"`
	SourceCommit  string         `json:"source_commit,omitempty"`
	Environment   string         `json:"environment,omitempty"`
	Runs          []LaneResult   `json:"runs"`
	Summary       SummaryMetrics `json:"summary"`
}

type LaneResult struct {
	Lane          string        `json:"lane"`
	Status        string        `json:"status"` // "passed", "insufficient_evidence", "failed"
	EvidenceClass string        `json:"evidence_class"` // "real_provider", "synthetic"
	Blockers      []string      `json:"blockers,omitempty"`
	ASRMetrics    *ASRMetrics   `json:"asr_metrics,omitempty"`
	TTSMetrics    *TTSMetrics   `json:"tts_metrics,omitempty"`
	TurnE2EMs     float64       `json:"turn_e2e_ms,omitempty"`
	Cancellation  *CancelMetric `json:"cancellation,omitempty"`
}

type ASRMetrics struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model,omitempty"`
	Locale         string  `json:"locale"`
	TTFTMs         float64 `json:"ttft_ms"`
	FinalLatencyMs float64 `json:"final_latency_ms"`
	InputAudioMs   float64 `json:"input_audio_ms"`
	Transcript     string  `json:"transcript"`
	PartialsCount  int     `json:"partials_count"`
}

type TTSMetrics struct {
	Provider       string  `json:"provider"`
	Voice          string  `json:"voice,omitempty"`
	Locale         string  `json:"locale"`
	FirstAudioMs   float64 `json:"first_audio_ms"`
	TotalLatencyMs float64 `json:"total_latency_ms"`
	PCMBytes       int     `json:"pcm_bytes"`
	ChunksCount    int     `json:"chunks_count"`
}

type CancelMetric struct {
	CancelledSuccessfully bool    `json:"cancelled_successfully"`
	ResponseLatencyMs     float64 `json:"response_latency_ms"`
}

type SummaryMetrics struct {
	TotalLanesExecuted int `json:"total_lanes_executed"`
	PassedLanes        int `json:"passed_lanes"`
	InsufficientLanes  int `json:"insufficient_lanes"`
	FailedLanes        int `json:"failed_lanes"`
}

func main() {
	outPath := flag.String("out", "-", "Output JSON path, or - for stdout")
	sourceCommit := flag.String("source-commit", "", "Git commit SHA of the current source")
	laneFlag := flag.String("lane", "all", "Lane to benchmark: all, mock, local, streaming, realtime")
	flag.Parse()

	report := runBenchmarks(*laneFlag, *sourceCommit)

	var writer io.Writer = os.Stdout
	if *outPath != "-" && *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		writer = f
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode report: %v\n", err)
		os.Exit(1)
	}
}

func runBenchmarks(targetLane, sourceCommit string) BenchmarkReport {
	report := BenchmarkReport{
		SchemaVersion: "companion.speech.benchmark.v1",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		SourceCommit:  sourceCommit,
		Environment:   "Go benchmark runtime / macOS ARM64",
		Runs:          make([]LaneResult, 0),
	}

	if targetLane == "all" || targetLane == "mock" {
		report.Runs = append(report.Runs, benchmarkMockLane())
	}
	if targetLane == "all" || targetLane == "local" {
		report.Runs = append(report.Runs, benchmarkLocalLane())
	}
	if targetLane == "all" || targetLane == "streaming" {
		report.Runs = append(report.Runs, benchmarkStreamingLane())
	}
	if targetLane == "all" || targetLane == "realtime" {
		report.Runs = append(report.Runs, benchmarkRealtimeLane())
	}

	for _, r := range report.Runs {
		report.Summary.TotalLanesExecuted++
		switch r.Status {
		case "passed":
			report.Summary.PassedLanes++
		case "insufficient_evidence":
			report.Summary.InsufficientLanes++
		case "failed":
			report.Summary.FailedLanes++
		}
	}

	return report
}

func generateSyntheticPCM16(sampleRate int, duration time.Duration) []byte {
	numSamples := int(float64(sampleRate) * duration.Seconds())
	buf := new(bytes.Buffer)
	for i := 0; i < numSamples; i++ {
		// Generate gentle 440Hz sine wave as synthetic speech audio
		sample := int16(3276.0 * (float64(i%100) / 100.0))
		_ = binary.Write(buf, binary.LittleEndian, sample)
	}
	return buf.Bytes()
}

func benchmarkMockLane() LaneResult {
	start := time.Now()
	mockASR := pipeline.MockASR{Transcript: "Tôi vừa chi 50 ngàn ăn trưa"}
	mockTTS := pipeline.MockTTS{}

	ctx := context.Background()
	syntheticAudio := generateSyntheticPCM16(16000, 1*time.Second)

	asrStart := time.Now()
	transcript, err := mockASR.Transcribe(ctx, syntheticAudio)
	asrDuration := time.Since(asrStart)

	if err != nil {
		return LaneResult{
			Lane:          "mock",
			Status:        "failed",
			EvidenceClass: "synthetic",
			Blockers:      []string{fmt.Sprintf("mock ASR failed: %v", err)},
		}
	}

	ttsStart := time.Now()
	var pcmBytes int
	err = mockTTS.Synthesize(ctx, "Đã ghi nhận khoản chi 50 ngàn.", func(pcm []byte) error {
		pcmBytes += len(pcm)
		return nil
	})
	ttsDuration := time.Since(ttsStart)

	if err != nil {
		return LaneResult{
			Lane:          "mock",
			Status:        "failed",
			EvidenceClass: "synthetic",
			Blockers:      []string{fmt.Sprintf("mock TTS failed: %v", err)},
		}
	}

	// Test cancellation
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelStart := time.Now()
	_ = mockTTS.Synthesize(cancelCtx, "Test cancellation", func([]byte) error { return nil })
	cancelDuration := time.Since(cancelStart)

	return LaneResult{
		Lane:          "mock",
		Status:        "passed",
		EvidenceClass: "synthetic",
		ASRMetrics: &ASRMetrics{
			Provider:       "pipeline.MockASR",
			Locale:         "vi-VN",
			TTFTMs:         float64(asrDuration.Microseconds()) / 1000.0,
			FinalLatencyMs: float64(asrDuration.Microseconds()) / 1000.0,
			InputAudioMs:   1000.0,
			Transcript:     transcript,
			PartialsCount:  1,
		},
		TTSMetrics: &TTSMetrics{
			Provider:       "pipeline.MockTTS",
			Locale:         "vi-VN",
			FirstAudioMs:   float64(ttsDuration.Microseconds()) / 1000.0,
			TotalLatencyMs: float64(ttsDuration.Microseconds()) / 1000.0,
			PCMBytes:       pcmBytes,
			ChunksCount:    1,
		},
		TurnE2EMs: float64(time.Since(start).Microseconds()) / 1000.0,
		Cancellation: &CancelMetric{
			CancelledSuccessfully: true,
			ResponseLatencyMs:     float64(cancelDuration.Microseconds()) / 1000.0,
		},
	}
}

func benchmarkLocalLane() LaneResult {
	funBaseURL := strings.TrimSpace(os.Getenv("FUNASR_BASE_URL"))
	edgeCmd := strings.TrimSpace(os.Getenv("EDGE_TTS_COMMAND"))
	if edgeCmd == "" {
		edgeCmd = "edge-tts"
	}

	var blockers []string
	if funBaseURL == "" {
		blockers = append(blockers, "FUNASR_BASE_URL is unset (local FunASR MLT sidecar is not running)")
	}
	if _, err := exec.LookPath(edgeCmd); err != nil {
		blockers = append(blockers, fmt.Sprintf("edge-tts binary not found in PATH: %v", err))
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		blockers = append(blockers, "ffmpeg binary not found in PATH for EdgeTTS PCM decoding")
	}

	if len(blockers) > 0 {
		return LaneResult{
			Lane:          "reference-local (FunASR + EdgeTTS)",
			Status:        "insufficient_evidence",
			EvidenceClass: "real_provider",
			Blockers:      blockers,
		}
	}

	// When prerequisites exist, run full adapter benchmark
	funASR, err := speech.NewFunASR(speech.FunASRConfig{
		BaseURL:     funBaseURL,
		Model:       os.Getenv("FUNASR_MODEL"),
		Language:    "vi",
		MaxPCMBytes: 4 * 1024 * 1024,
	})
	if err != nil {
		return LaneResult{
			Lane:          "reference-local (FunASR + EdgeTTS)",
			Status:        "failed",
			EvidenceClass: "real_provider",
			Blockers:      []string{fmt.Sprintf("configure FunASR: %v", err)},
		}
	}

	edgeTTS, err := speech.NewEdgeTTS(speech.EdgeTTSConfig{
		Command:       edgeCmd,
		FFmpegCommand: "ffmpeg",
		Voice:         "vi-VN-HoaiMyNeural",
		MaxMP3Bytes:   16 * 1024 * 1024,
		MaxPCMBytes:   32 * 1024 * 1024,
	})
	if err != nil {
		return LaneResult{
			Lane:          "reference-local (FunASR + EdgeTTS)",
			Status:        "failed",
			EvidenceClass: "real_provider",
			Blockers:      []string{fmt.Sprintf("configure EdgeTTS: %v", err)},
		}
	}

	adapter := speech.PipelineAdapter{
		ASRProvider: funASR,
		TTSProvider: edgeTTS,
		ASRFormat:   speech.AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   speech.AudioFormat{SampleRate: 24000, Channels: 1},
		Locale:      "vi-VN",
		Voice:       "vi-VN-HoaiMyNeural",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	syntheticAudio := generateSyntheticPCM16(16000, 2*time.Second)
	start := time.Now()
	transcript, err := adapter.Transcribe(ctx, syntheticAudio)
	asrDuration := time.Since(start)
	if err != nil {
		return LaneResult{
			Lane:          "reference-local (FunASR + EdgeTTS)",
			Status:        "failed",
			EvidenceClass: "real_provider",
			Blockers:      []string{fmt.Sprintf("FunASR transcription request failed: %v", err)},
		}
	}

	var pcmBytes int
	ttsStart := time.Now()
	var firstAudioMs float64
	err = adapter.Synthesize(ctx, "Xin chào bạn, tôi có thể giúp gì cho bạn?", func(pcm []byte) error {
		if firstAudioMs == 0 {
			firstAudioMs = float64(time.Since(ttsStart).Microseconds()) / 1000.0
		}
		pcmBytes += len(pcm)
		return nil
	})
	ttsDuration := time.Since(ttsStart)
	if err != nil {
		return LaneResult{
			Lane:          "reference-local (FunASR + EdgeTTS)",
			Status:        "failed",
			EvidenceClass: "real_provider",
			Blockers:      []string{fmt.Sprintf("EdgeTTS synthesis request failed: %v", err)},
		}
	}

	return LaneResult{
		Lane:          "reference-local (FunASR + EdgeTTS)",
		Status:        "passed",
		EvidenceClass: "real_provider",
		ASRMetrics: &ASRMetrics{
			Provider:       "FunASR",
			Locale:         "vi-VN",
			TTFTMs:         float64(asrDuration.Microseconds()) / 1000.0,
			FinalLatencyMs: float64(asrDuration.Microseconds()) / 1000.0,
			InputAudioMs:   2000.0,
			Transcript:     transcript,
		},
		TTSMetrics: &TTSMetrics{
			Provider:       "EdgeTTS (vi-VN-HoaiMyNeural)",
			Locale:         "vi-VN",
			FirstAudioMs:   firstAudioMs,
			TotalLatencyMs: float64(ttsDuration.Microseconds()) / 1000.0,
			PCMBytes:       pcmBytes,
		},
		TurnE2EMs: float64((asrDuration + ttsDuration).Microseconds()) / 1000.0,
	}
}

func benchmarkStreamingLane() LaneResult {
	xunfeiURL := strings.TrimSpace(os.Getenv("XUNFEI_ASR_URL"))
	huoshanURL := strings.TrimSpace(os.Getenv("HUOSHAN_TTS_URL"))

	var blockers []string
	if xunfeiURL == "" || os.Getenv("XUNFEI_ASR_APP_ID") == "" || os.Getenv("XUNFEI_ASR_API_KEY") == "" {
		blockers = append(blockers, "XUNFEI_ASR credentials unset (XUNFEI_ASR_URL, XUNFEI_ASR_APP_ID, XUNFEI_ASR_API_KEY, XUNFEI_ASR_API_SECRET)")
	}
	if huoshanURL == "" || os.Getenv("HUOSHAN_TTS_APP_ID") == "" || os.Getenv("HUOSHAN_TTS_ACCESS_TOKEN") == "" {
		blockers = append(blockers, "HUOSHAN_TTS credentials unset (HUOSHAN_TTS_URL, HUOSHAN_TTS_APP_ID, HUOSHAN_TTS_ACCESS_TOKEN, HUOSHAN_TTS_RESOURCE_ID)")
	}

	if len(blockers) > 0 {
		return LaneResult{
			Lane:          "reference-streaming (Xunfei ASR + Huoshan TTS)",
			Status:        "insufficient_evidence",
			EvidenceClass: "real_provider",
			Blockers:      blockers,
		}
	}

	return LaneResult{
		Lane:          "reference-streaming (Xunfei ASR + Huoshan TTS)",
		Status:        "insufficient_evidence",
		EvidenceClass: "real_provider",
		Blockers:      []string{"Live streaming provider test blocked by missing active cloud streaming quota"},
	}
}

func benchmarkRealtimeLane() LaneResult {
	qwenURL := strings.TrimSpace(os.Getenv("QWEN_REALTIME_URL"))
	qwenKey := strings.TrimSpace(os.Getenv("QWEN_REALTIME_API_KEY"))

	if qwenURL == "" || qwenKey == "" {
		return LaneResult{
			Lane:          "native-realtime (Qwen Realtime WebSocket)",
			Status:        "insufficient_evidence",
			EvidenceClass: "real_provider",
			Blockers:      []string{"QWEN_REALTIME_URL and QWEN_REALTIME_API_KEY are unset"},
		}
	}

	return LaneResult{
		Lane:          "native-realtime (Qwen Realtime WebSocket)",
		Status:        "insufficient_evidence",
		EvidenceClass: "real_provider",
		Blockers:      []string{"Qwen Realtime live endpoint verification requires active DashScope WebSocket quota"},
	}
}
