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
	"time"

	"companion-server/eval/speechbench"
	"companion-server/internal/speech"
)

const inputRate = 16000

type options struct {
	corpus string
	out    string
	commit string
	runner string
	pace   bool
}

type receiveResult struct {
	event speech.NativeRealtimeEvent
	err   error
}

func main() {
	var o options
	flag.StringVar(&o.corpus, "corpus", "", "resolved corpus manifest")
	flag.StringVar(&o.out, "out", "gemini-live-report.json", "output report")
	flag.StringVar(&o.commit, "commit", env("GITHUB_SHA", "unknown"), "source commit")
	flag.StringVar(&o.runner, "runner", runtime.GOOS+"/"+runtime.GOARCH, "runner description")
	flag.BoolVar(&o.pace, "pace", true, "pace PCM at recorded speed")
	flag.Parse()
	if strings.TrimSpace(o.corpus) == "" {
		fatal(errors.New("--corpus is required"))
	}
	corpus, err := speechbench.LoadCorpus(o.corpus)
	if err != nil {
		fatal(err)
	}
	manifestHash, err := fileSHA256(o.corpus)
	if err != nil {
		fatal(err)
	}
	report := speechbench.NewReport(o.commit, o.runner, manifestHash, corpus)
	report.Limitations = []string{
		"Provider quality uses the same content-addressed VN/EN/mixed FLEURS corpus as the cascade reference lane; the mixed case is a deterministic concatenation rather than natural conversational code-switch audio.",
		"Gemini Live timings are provider-direct reference measurements. Canonical authenticated /v2/device + ADK + ToolRegistry evidence is emitted separately and must not be inferred from this report.",
		"Input and output audio transcription are enabled for evidence. Gemini pricing documentation states transcription text tokens are additive to normal audio costs.",
		"Recorded fixtures do not prove physical microphone, enclosure, AEC, WakeNet or speaker quality.",
	}
	lane := runGemini(corpus, o.pace)
	report.Lanes = []speechbench.LaneResult{lane}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(o.out, append(raw, '\n'), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", o.out)
	fmt.Printf("lane=%s status=%s cases=%d blockers=%d failures=%d\n", lane.Lane, lane.Status, len(lane.Cases), len(lane.Blockers), len(lane.Failures))
}

func runGemini(corpus speechbench.CorpusManifest, pace bool) speechbench.LaneResult {
	key := strings.TrimSpace(os.Getenv("GEMINI_TOKEN"))
	model := env("GEMINI_LIVE_MODEL", "gemini-3.1-flash-live-preview")
	voice := env("GEMINI_LIVE_VOICE", "Kore")
	lane := speechbench.LaneResult{
		Lane:          "native-realtime-gemini-live",
		EvidenceClass: "real_provider_recorded_fixture",
		Provenance: []speechbench.ProviderProvenance{{
			Provider:      "Google Gemini Developer API Live",
			Model:         model,
			Voice:         voice,
			Region:        "Google-managed",
			EndpointClass: "native_websocket_v1beta",
			Config: map[string]string{
				"input_audio":               "pcm16-mono-16khz",
				"output_audio":              "native-audio pcm24khz",
				"turn_detection":            "manual activityStart/activityEnd",
				"activity_handling":         "START_OF_ACTIVITY_INTERRUPTS",
				"input_audio_transcription": "enabled",
				"output_audio_transcription": "enabled",
			},
			QuotaNote:   env("GEMINI_LIVE_QUOTA_NOTE", "Free-tier access is account/project dependent; effective live preview rate limits are enforced by the provider and were not inferred by the runner."),
			PricingNote: env("GEMINI_LIVE_PRICING_NOTE", "Official Gemini Developer API pricing lists free-tier input/output as free; paid audio is $0.005/min input and $0.018/min output for gemini-3.1-flash-live-preview. Transcription text tokens are additional when enabled."),
			PrivacyNote: "Recorded corpus audio and generated output are sent to Google Gemini Developer API. Official pricing documentation states free-tier content may be used to improve Google products; paid tier states it is not.",
		}},
	}
	if key == "" {
		lane.Status = "insufficient_evidence"
		lane.Blockers = []string{"GEMINI_TOKEN is not configured"}
		return lane
	}
	factory := func() (speech.NativeRealtimeProvider, error) {
		return speech.NewGeminiLive(speech.GeminiLiveConfig{
			Model:        model,
			APIKey:       key,
			Voice:        voice,
			Instructions: "Respond briefly and naturally in the language used by the speaker. Do not use tools in this provider-direct quality benchmark.",
		})
	}
	for _, c := range corpus.Cases {
		provider, err := factory()
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		lane.Cases = append(lane.Cases, runCase(c, provider, pace))
	}
	lane.Summary = speechbench.Summarize(lane.Cases)
	lane.Cancellation = runCancellation(factory, firstCase(corpus, "en"), pace)
	lane.Reconnect = runReconnect(factory)
	finishLane(&lane)
	return lane
}

func runCase(c speechbench.CorpusCase, provider speech.NativeRealtimeProvider, pace bool) speechbench.CaseResult {
	result := baseCase(c)
	pcm, err := verifiedPCM(c)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	session, err := provider.Connect(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer session.Close()

	received := make(chan receiveResult, 64)
	go func() {
		for {
			event, receiveErr := session.Receive(ctx)
			select {
			case received <- receiveResult{event: event, err: receiveErr}:
			case <-ctx.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()

	inputStarted := time.Now()
	if err := appendPCM(ctx, session, pcm, pace); err != nil {
		result.Error = err.Error()
		return result
	}
	commitAt := time.Now()
	if err := session.CommitAudio(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := session.CreateResponse(ctx); err != nil {
		result.Error = err.Error()
		return result
	}

	var firstInput *float64
	var transcript string
	var asrFinalAt, firstAudioAt, audioDoneAt, responseDoneAt time.Time
	bytes, chunks := 0, 0
	for responseDoneAt.IsZero() {
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			return result
		case item := <-received:
			if item.err != nil {
				result.Error = item.err.Error()
				return result
			}
			now := time.Now()
			e := item.event
			if e.InputTranscript != "" {
				transcript = e.InputTranscript
				if firstInput == nil {
					v := milliseconds(now.Sub(inputStarted))
					firstInput = &v
				}
			}
			if e.InputFinal && asrFinalAt.IsZero() {
				asrFinalAt = now
			}
			if len(e.AudioPCM) > 0 {
				if firstAudioAt.IsZero() {
					firstAudioAt = now
				}
				chunks++
				bytes += len(e.AudioPCM)
			}
			if e.Type == "response.audio.done" && audioDoneAt.IsZero() {
				audioDoneAt = now
			}
			if e.ResponseDone {
				responseDoneAt = now
			}
		}
	}
	if asrFinalAt.IsZero() || strings.TrimSpace(transcript) == "" {
		result.Error = "Gemini Live completed without final input transcription"
		return result
	}
	if firstAudioAt.IsZero() {
		result.Error = "Gemini Live completed without audio"
		return result
	}
	result.Transcript = transcript
	result.ASRFirstPartialMS = firstInput
	result.ASRFinalMS = max0(milliseconds(asrFinalAt.Sub(commitAt)))
	result.WER = speechbench.WER(c.Reference, transcript)
	result.CER = speechbench.CER(c.Reference, transcript)
	result.TTSFirstAudioMS = max0(milliseconds(firstAudioAt.Sub(commitAt)))
	if !audioDoneAt.IsZero() {
		result.TTSTotalMS = max0(milliseconds(audioDoneAt.Sub(firstAudioAt)))
	}
	result.TTSPCMBytes = bytes
	result.TTSChunks = chunks
	result.SpeechToFirstAudioMS = c.DurationMS + result.TTSFirstAudioMS
	turn := milliseconds(responseDoneAt.Sub(inputStarted))
	result.TurnE2EMS = &turn
	return result
}

func runCancellation(factory func() (speech.NativeRealtimeProvider, error), c speechbench.CorpusCase, pace bool) *speechbench.CancellationResult {
	result := &speechbench.CancellationResult{Attempted: true}
	provider, err := factory()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	pcm, err := verifiedPCM(c)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	session, err := provider.Connect(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer session.Close()
	if err := appendPCM(ctx, session, pcm, pace); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := session.CommitAudio(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := session.CreateResponse(ctx); err != nil {
		result.Error = err.Error()
		return result
	}
	cancelAt := time.Time{}
	cancelSent := false
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		if len(event.AudioPCM) > 0 {
			if !cancelSent {
				cancelSent = true
				cancelAt = time.Now()
				if err := session.CancelResponse(ctx); err != nil {
					result.Error = err.Error()
					return result
				}
				continue
			}
			result.StaleAudioChunks++
		}
		if cancelSent && event.ResponseDone {
			result.CancelLatencyMS = milliseconds(time.Since(cancelAt))
			result.Cancelled = event.ResponseStatus == "cancelled" && result.StaleAudioChunks == 0
			result.Evidence = "manual activityStart interrupted active Gemini Live generation; stale provider audio chunks were counted until interrupted"
			if event.ResponseStatus != "cancelled" {
				result.Error = "Gemini Live completed without interrupted status after cancellation"
			}
			return result
		}
	}
}

func runReconnect(factory func() (speech.NativeRealtimeProvider, error)) *speechbench.ReconnectResult {
	result := &speechbench.ReconnectResult{Attempted: true}
	for attempt := 1; attempt <= 2; attempt++ {
		provider, err := factory()
		if err != nil {
			result.Error = err.Error()
			return result
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		session, err := provider.Connect(ctx)
		if err == nil {
			err = session.Close()
		}
		cancel()
		if err != nil {
			result.Error = fmt.Sprintf("connect %d: %v", attempt, err)
			return result
		}
	}
	result.Passed = true
	result.Evidence = "two independent authenticated Gemini Live websocket sessions completed setup and closed; fresh-session recovery is proven, while stateful session-resumption handles are recorded but not required by this evidence slice"
	return result
}

func appendPCM(ctx context.Context, session speech.NativeRealtimeSession, pcm []byte, pace bool) error {
	// 40 ms of PCM16 mono at 16 kHz, matching current Gemini Live guidance for
	// small realtime chunks while remaining deterministic across providers.
	const chunk = 1280
	for offset := 0; offset < len(pcm); offset += chunk {
		end := offset + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := session.AppendAudio(ctx, pcm[offset:end]); err != nil {
			return err
		}
		if pace {
			delay := time.Duration(float64(time.Second) * float64(end-offset) / float64(inputRate*2))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
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
	if lane.Cancellation == nil || !lane.Cancellation.Cancelled {
		lane.Status = "failed"
		return
	}
	if lane.Reconnect == nil || !lane.Reconnect.Passed {
		lane.Status = "failed"
		return
	}
	lane.Status = "passed"
}

func baseCase(c speechbench.CorpusCase) speechbench.CaseResult {
	return speechbench.CaseResult{ID: c.ID, Language: c.Language, Reference: c.Reference, InputAudioMS: c.DurationMS}
}

func errorCase(c speechbench.CorpusCase, err error) speechbench.CaseResult {
	result := baseCase(c)
	result.Error = err.Error()
	return result
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

func firstCase(corpus speechbench.CorpusManifest, language string) speechbench.CorpusCase {
	for _, c := range corpus.Cases {
		if c.Language == language {
			return c
		}
	}
	return corpus.Cases[0]
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
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000
}

func max0(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench-gemini-live:", err)
	os.Exit(2)
}
