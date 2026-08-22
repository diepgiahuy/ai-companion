package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"companion-server/eval/speechbench"
	"companion-server/internal/speech"
)

const (
	inputRate  = 16000
	outputRate = 24000
)

type options struct {
	corpus string
	out    string
	commit string
	runner string
}

func main() {
	var o options
	flag.StringVar(&o.corpus, "corpus", "", "resolved corpus manifest")
	flag.StringVar(&o.out, "out", "speech-provider-report.json", "output report")
	flag.StringVar(&o.commit, "commit", env("GITHUB_SHA", "unknown"), "source commit")
	flag.StringVar(&o.runner, "runner", runtime.GOOS+"/"+runtime.GOARCH, "runner description")
	flag.Parse()
	if strings.TrimSpace(o.corpus) == "" {
		fatal(errors.New("--corpus is required"))
	}
	manifest, err := speechbench.LoadCorpus(o.corpus)
	if err != nil {
		fatal(err)
	}
	manifestHash, err := fileSHA256(o.corpus)
	if err != nil {
		fatal(err)
	}
	report := speechbench.NewReport(o.commit, o.runner, manifestHash, manifest)
	report.Limitations = []string{
		"Provider quality uses content-addressed human-recorded FLEURS audio normalized to 16-kHz mono PCM. Dataset Viewer rows are not revision-addressable, so the immutable comparison identity is the emitted manifest SHA-256 plus per-case PCM SHA-256 values; this does not prove physical microphone, enclosure, AEC, WakeNet or speaker quality.",
		"Cascade speech_to_first_audio_ms covers recorded utterance duration + ASR finalization + deterministic-response TTS first audio. It intentionally excludes LLM/ADK time and is not reported as full turn_e2e_ms.",
		"The mixed case is a deterministic concatenation of Vietnamese and English human recordings, labelled composite_recorded; it is not evidence for natural conversational code-switch acoustics.",
		"Provider-lane timings call the reference speech adapters directly. Canonical Companion /v2/device + ADK/ToolRegistry evidence is a separate acceptance surface and must not be inferred from these timings.",
		"Gemini Live native-realtime evidence is produced by the dedicated bench-gemini-live workflow using the same content-addressed corpus and speechbench report schema.",
	}
	report.Lanes = append(report.Lanes, runLocal(manifest))
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(o.out, append(raw, '\n'), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", o.out)
	for _, lane := range report.Lanes {
		fmt.Printf("lane=%s status=%s cases=%d blockers=%d failures=%d\n", lane.Lane, lane.Status, len(lane.Cases), len(lane.Blockers), len(lane.Failures))
	}
}

func runLocal(corpus speechbench.CorpusManifest) speechbench.LaneResult {
	base := strings.TrimSpace(os.Getenv("FUNASR_BASE_URL"))
	model := env("FUNASR_MODEL", "FunAudioLLM/Fun-ASR-MLT-Nano-2512")
	lane := speechbench.LaneResult{Lane: "reference-local", EvidenceClass: "real_provider_recorded_fixture", Provenance: []speechbench.ProviderProvenance{
		{
			Provider:      "FunASR OpenAI-compatible local server",
			Model:         model,
			Region:        "local",
			EndpointClass: "localhost_http",
			Config: map[string]string{
				"funasr_version":     env("FUNASR_RUNTIME_VERSION", "unknown"),
				"remote_code_commit": env("FUNASR_REMOTE_CODE_COMMIT", "unknown"),
			},
			PrivacyNote: "ASR audio stays on the benchmark runner when FUNASR_BASE_URL points to localhost.",
		},
		{
			Provider:      "edge-tts client + ffmpeg decode",
			Model:         "Microsoft Edge online speech service",
			Voice:         "per-language explicit voice",
			EndpointClass: "edge_tts_cli",
			Config: map[string]string{
				"edge_tts_version": env("EDGE_TTS_VERSION", "unknown"),
				"ffmpeg_version":   env("FFMPEG_VERSION", "unknown"),
			},
			PrivacyNote: "TTS text is sent by the edge-tts client to its upstream online speech service.",
		},
	}}
	if base == "" {
		lane.Status = "insufficient_evidence"
		lane.Blockers = []string{"FUNASR_BASE_URL is not configured; start the pinned local FunASR server before running reference-local."}
		return lane
	}
	for _, c := range corpus.Cases {
		asr, err := speech.NewFunASR(speech.FunASRConfig{BaseURL: base, Model: model, Language: funASRLanguage(c.Language)})
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		voice := edgeVoice(c.Language)
		tts, err := speech.NewEdgeTTS(speech.EdgeTTSConfig{Voice: voice})
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		lane.Cases = append(lane.Cases, runCascadeCase(c, asr, tts, voice))
	}
	lane.Summary = speechbench.Summarize(lane.Cases)
	lane.Cancellation = runTTSCancel(func() (speech.StreamingTTSProvider, error) {
		return speech.NewEdgeTTS(speech.EdgeTTSConfig{Voice: edgeVoice("vi")})
	}, firstCase(corpus, "vi"))
	lane.Reconnect = runASRReconnect(func(c speechbench.CorpusCase) (speech.StreamingASRProvider, error) {
		return speech.NewFunASR(speech.FunASRConfig{BaseURL: base, Model: model, Language: funASRLanguage(c.Language)})
	}, firstCase(corpus, "en"))
	finishLane(&lane)
	return lane
}

func runCascadeCase(c speechbench.CorpusCase, asr speech.StreamingASRProvider, tts speech.StreamingTTSProvider, voice string) speechbench.CaseResult {
	result := baseCase(c)
	pcm, err := verifiedPCM(c)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	streamStart := time.Now()
	var firstPartial *float64
	var mu sync.Mutex
	stream, err := asr.StartASR(context.Background(), speech.ASRRequest{Format: speech.AudioFormat{SampleRate: inputRate, Channels: 1}, Locale: c.Language}, func(e speech.TranscriptEvent) error {
		if !e.Final {
			mu.Lock()
			if firstPartial == nil {
				v := ms(time.Since(streamStart))
				firstPartial = &v
			}
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer stream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := pushPCM(ctx, stream, pcm); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := stream.CloseInput(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	finalStart := time.Now()
	transcript, err := stream.Wait(ctx)
	result.ASRFinalMS = ms(time.Since(finalStart))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Transcript = transcript
	result.WER = speechbench.WER(c.Reference, transcript)
	result.CER = speechbench.CER(c.Reference, transcript)
	result.ASRFirstPartialMS = firstPartial

	ttsStart := time.Now()
	firstAudio := -1.0
	chunks := 0
	bytes := 0
	err = tts.Synthesize(ctx, speech.TTSRequest{Text: c.TTSResponse, Voice: voice, Locale: c.Language, Format: speech.AudioFormat{SampleRate: outputRate, Channels: 1}, TurnID: c.ID}, func(e speech.AudioEvent) error {
		if len(e.PCM) > 0 {
			if firstAudio < 0 {
				firstAudio = ms(time.Since(ttsStart))
			}
			chunks++
			bytes += len(e.PCM)
		}
		return nil
	})
	result.TTSTotalMS = ms(time.Since(ttsStart))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if firstAudio < 0 {
		result.Error = "TTS returned no PCM"
		return result
	}
	result.TTSFirstAudioMS = firstAudio
	result.TTSPCMBytes = bytes
	result.TTSChunks = chunks
	result.SpeechToFirstAudioMS = c.DurationMS + result.ASRFinalMS + result.TTSFirstAudioMS
	return result
}

func runTTSCancel(factory func() (speech.StreamingTTSProvider, error), c speechbench.CorpusCase) *speechbench.CancellationResult {
	res := &speechbench.CancellationResult{Attempted: true}
	provider, err := factory()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := false
	cancelAt := time.Time{}
	chunksAfter := 0
	err = provider.Synthesize(ctx, speech.TTSRequest{Text: c.TTSResponse, Locale: c.Language, Format: speech.AudioFormat{SampleRate: outputRate, Channels: 1}, TurnID: "cancel-" + c.ID}, func(e speech.AudioEvent) error {
		if len(e.PCM) == 0 {
			return nil
		}
		if !first {
			first = true
			cancelAt = time.Now()
			cancel()
			return nil
		}
		chunksAfter++
		return nil
	})
	res.StaleAudioChunks = chunksAfter
	if first {
		res.CancelLatencyMS = ms(time.Since(cancelAt))
	}
	if errors.Is(err, context.Canceled) {
		res.Cancelled = true
		res.Evidence = "context cancellation stopped synthesis after first emitted audio chunk"
		return res
	}
	if err == nil && first {
		res.Cancelled = chunksAfter == 0
		res.Evidence = "synthesis completed at or immediately after cancellation boundary"
		return res
	}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

func runASRReconnect(factory func(speechbench.CorpusCase) (speech.StreamingASRProvider, error), c speechbench.CorpusCase) *speechbench.ReconnectResult {
	res := &speechbench.ReconnectResult{Attempted: true}
	for i := 0; i < 2; i++ {
		p, err := factory(c)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		pcm, err := verifiedPCM(c)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		stream, err := p.StartASR(ctx, speech.ASRRequest{Format: speech.AudioFormat{SampleRate: inputRate, Channels: 1}, Locale: c.Language}, func(speech.TranscriptEvent) error { return nil })
		if err == nil {
			err = pushPCM(ctx, stream, pcm)
		}
		if err == nil {
			err = stream.CloseInput(ctx)
		}
		if err == nil {
			_, err = stream.Wait(ctx)
		}
		if stream != nil {
			_ = stream.Close()
		}
		cancel()
		if err != nil {
			res.Error = fmt.Sprintf("attempt %d: %v", i+1, err)
			return res
		}
	}
	res.Passed = true
	res.Evidence = "two independent ASR sessions completed on the same recorded case"
	return res
}

func finishLane(lane *speechbench.LaneResult) {
	for _, c := range lane.Cases {
		if c.Error != "" {
			lane.Failures = append(lane.Failures, c.ID+": "+c.Error)
		}
	}
	if len(lane.Failures) > 0 {
		lane.Status = "insufficient_evidence"
		return
	}
	if lane.Cancellation != nil && lane.Cancellation.Attempted && !lane.Cancellation.Cancelled {
		lane.Status = "failed"
		return
	}
	if lane.Reconnect != nil && lane.Reconnect.Attempted && !lane.Reconnect.Passed {
		lane.Status = "failed"
		return
	}
	lane.Status = "passed"
}

func baseCase(c speechbench.CorpusCase) speechbench.CaseResult {
	return speechbench.CaseResult{ID: c.ID, Language: c.Language, Reference: c.Reference, InputAudioMS: c.DurationMS}
}

func errorCase(c speechbench.CorpusCase, err error) speechbench.CaseResult {
	r := baseCase(c)
	r.Error = err.Error()
	return r
}

func verifiedPCM(c speechbench.CorpusCase) ([]byte, error) {
	raw, err := os.ReadFile(c.PCMPath)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw)%2 != 0 {
		return nil, fmt.Errorf("%s invalid PCM length %d", c.ID, len(raw))
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != c.PCM_SHA256 {
		return nil, fmt.Errorf("%s PCM hash mismatch got=%s want=%s", c.ID, got, c.PCM_SHA256)
	}
	return raw, nil
}

func pushPCM(ctx context.Context, s speech.ASRStream, pcm []byte) error {
	const chunk = 1280
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := s.Push(ctx, pcm[off:end]); err != nil {
			return err
		}
	}
	return nil
}

func firstCase(c speechbench.CorpusManifest, language string) speechbench.CorpusCase {
	for _, v := range c.Cases {
		if v.Language == language {
			return v
		}
	}
	return c.Cases[0]
}

func funASRLanguage(language string) string {
	switch language {
	case "vi":
		return "vi"
	case "en":
		return "en"
	default:
		return ""
	}
}

func edgeVoice(language string) string {
	switch language {
	case "en":
		return env("EDGE_TTS_VOICE_EN", "en-US-JennyNeural")
	case "mixed":
		return env("EDGE_TTS_VOICE_MIXED", env("EDGE_TTS_VOICE_VI", "vi-VN-HoaiMyNeural"))
	default:
		return env("EDGE_TTS_VOICE_VI", "vi-VN-HoaiMyNeural")
	}
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench-speech:", err)
	os.Exit(2)
}
