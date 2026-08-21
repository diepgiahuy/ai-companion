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
	"sort"
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
	lanes  string
	commit string
	runner string
	pace   bool
}

type realtimeReceive struct {
	event speech.NativeRealtimeEvent
	err   error
}

func main() {
	var o options
	flag.StringVar(&o.corpus, "corpus", "", "resolved corpus manifest")
	flag.StringVar(&o.out, "out", "speech-provider-report.json", "output report")
	flag.StringVar(&o.lanes, "lanes", "local,streaming,realtime", "comma-separated lanes")
	flag.StringVar(&o.commit, "commit", env("GITHUB_SHA", "unknown"), "source commit")
	flag.StringVar(&o.runner, "runner", runtime.GOOS+"/"+runtime.GOARCH, "runner description")
	flag.BoolVar(&o.pace, "pace", true, "pace streaming/realtime PCM at recorded speed")
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
		"Native realtime turn_e2e_ms covers paced recorded input through provider response.done. ASR final latency is captured at input_audio_transcription.completed and first-audio latency is measured from input commit; these are not inferred from response completion.",
		"The mixed case is a deterministic concatenation of Vietnamese and English human recordings, labelled composite_recorded; it is not evidence for natural conversational code-switch acoustics.",
		"Provider-lane timings call the production speech/realtime adapters directly. Canonical Companion /v2/device + ADK/ToolRegistry evidence is a separate acceptance surface and must not be inferred from these timings.",
		"Unavailable credentials, account language entitlements or quota are recorded as insufficient_evidence rather than replaced with mocks.",
	}
	for _, lane := range parseLanes(o.lanes) {
		switch lane {
		case "local":
			report.Lanes = append(report.Lanes, runLocal(manifest, o.pace))
		case "streaming":
			report.Lanes = append(report.Lanes, runStreaming(manifest, o.pace))
		case "realtime":
			report.Lanes = append(report.Lanes, runRealtime(manifest, o.pace))
		default:
			fatal(fmt.Errorf("unknown lane %q", lane))
		}
	}
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

func runLocal(corpus speechbench.CorpusManifest, pace bool) speechbench.LaneResult {
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
		lane.Cases = append(lane.Cases, runCascadeCase(c, asr, tts, false, voice))
	}
	lane.Summary = speechbench.Summarize(lane.Cases)
	lane.Cancellation = runTTSCancel(func() (speech.StreamingTTSProvider, error) {
		return speech.NewEdgeTTS(speech.EdgeTTSConfig{Voice: edgeVoice("vi")})
	}, firstCase(corpus, "vi"))
	lane.Reconnect = runASRReconnect(func(c speechbench.CorpusCase) (speech.StreamingASRProvider, error) {
		return speech.NewFunASR(speech.FunASRConfig{BaseURL: base, Model: model, Language: funASRLanguage(c.Language)})
	}, firstCase(corpus, "en"), false)
	finishLane(&lane)
	_ = pace
	return lane
}

func runStreaming(corpus speechbench.CorpusManifest, pace bool) speechbench.LaneResult {
	lane := speechbench.LaneResult{Lane: "reference-streaming", EvidenceClass: "real_provider_recorded_fixture"}
	appID, key, secret := os.Getenv("XUNFEI_ASR_APP_ID"), os.Getenv("XUNFEI_ASR_API_KEY"), os.Getenv("XUNFEI_ASR_API_SECRET")
	hApp, hToken, hResource, hSpeaker := os.Getenv("HUOSHAN_TTS_APP_ID"), os.Getenv("HUOSHAN_TTS_ACCESS_TOKEN"), os.Getenv("HUOSHAN_TTS_RESOURCE_ID"), os.Getenv("HUOSHAN_TTS_SPEAKER")
	missing := missingNames(map[string]string{
		"XUNFEI_ASR_APP_ID":         appID,
		"XUNFEI_ASR_API_KEY":        key,
		"XUNFEI_ASR_API_SECRET":     secret,
		"HUOSHAN_TTS_APP_ID":        hApp,
		"HUOSHAN_TTS_ACCESS_TOKEN":  hToken,
		"HUOSHAN_TTS_RESOURCE_ID":   hResource,
		"HUOSHAN_TTS_SPEAKER":       hSpeaker,
	})
	lane.Provenance = []speechbench.ProviderProvenance{
		{Provider: "Xunfei streaming IAT v2", Model: "iat", Region: env("XUNFEI_ASR_REGION", "account-defined"), EndpointClass: "wss_streaming", QuotaNote: env("XUNFEI_ASR_QUOTA_NOTE", "unknown; inspect provider account"), PricingNote: env("XUNFEI_ASR_PRICING_NOTE", "unknown; inspect provider account"), PrivacyNote: "Recorded corpus audio is sent to Xunfei for this lane."},
		{Provider: "Huoshan/Volcengine bidirectional TTS", Model: env("HUOSHAN_TTS_RESOURCE_ID", ""), Voice: hSpeaker, Region: env("HUOSHAN_TTS_REGION", "account-defined"), EndpointClass: "wss_bidirectional", QuotaNote: env("HUOSHAN_TTS_QUOTA_NOTE", "unknown; inspect provider account"), PricingNote: env("HUOSHAN_TTS_PRICING_NOTE", "unknown; inspect provider account"), PrivacyNote: "Benchmark response text is sent to Huoshan/Volcengine for this lane."},
	}
	if len(missing) > 0 {
		lane.Status = "insufficient_evidence"
		lane.Blockers = []string{"missing GitHub/provider credentials: " + strings.Join(missing, ", ")}
		return lane
	}
	ttsURL := env("HUOSHAN_TTS_URL", "wss://openspeech.bytedance.com/api/v3/tts/bidirection")
	for _, c := range corpus.Cases {
		lang, url, blocker := xunfeiLanguage(c.Language)
		if blocker != "" {
			lane.Cases = append(lane.Cases, errorCase(c, errors.New(blocker)))
			continue
		}
		asr, err := speech.NewXunfeiStreamASR(speech.XunfeiStreamASRConfig{URL: url, AppID: appID, APIKey: key, APISecret: secret, Language: lang, DynamicCorrection: lang == "zh_cn"})
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		tts, err := speech.NewHuoshanDoubleStreamTTS(speech.HuoshanDoubleStreamTTSConfig{URL: ttsURL, AppID: hApp, AccessToken: hToken, ResourceID: hResource, Speaker: hSpeaker})
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		lane.Cases = append(lane.Cases, runCascadeCase(c, asr, tts, pace, hSpeaker))
	}
	lane.Summary = speechbench.Summarize(lane.Cases)
	lane.Cancellation = runTTSCancel(func() (speech.StreamingTTSProvider, error) {
		return speech.NewHuoshanDoubleStreamTTS(speech.HuoshanDoubleStreamTTSConfig{URL: ttsURL, AppID: hApp, AccessToken: hToken, ResourceID: hResource, Speaker: hSpeaker})
	}, firstCase(corpus, "en"))
	lane.Reconnect = runASRReconnect(func(c speechbench.CorpusCase) (speech.StreamingASRProvider, error) {
		lang, url, blocker := xunfeiLanguage(c.Language)
		if blocker != "" {
			return nil, errors.New(blocker)
		}
		return speech.NewXunfeiStreamASR(speech.XunfeiStreamASRConfig{URL: url, AppID: appID, APIKey: key, APISecret: secret, Language: lang})
	}, firstCase(corpus, "en"), pace)
	finishLane(&lane)
	return lane
}

func runRealtime(corpus speechbench.CorpusManifest, pace bool) speechbench.LaneResult {
	key, workspace := os.Getenv("DASHSCOPE_REALTIME_KEY"), os.Getenv("DASHSCOPE_WORKSPACE_ID")
	model := env("DASHSCOPE_REALTIME_MODEL", "qwen3.5-omni-flash-realtime")
	voice := env("DASHSCOPE_REALTIME_VOICE", "Tina")
	region := env("DASHSCOPE_REALTIME_REGION", "cn-beijing")
	lane := speechbench.LaneResult{Lane: "native-realtime", EvidenceClass: "real_provider_recorded_fixture", Provenance: []speechbench.ProviderProvenance{{Provider: "Alibaba Cloud Model Studio Qwen Omni Realtime", Model: model, Voice: voice, Region: region, EndpointClass: "native_websocket", Config: map[string]string{"input_audio_transcription": "qwen3-asr-flash-realtime", "input_audio": "pcm16-mono-16khz", "output_audio": "pcm16-mono-24khz", "turn_detection": "manual"}, QuotaNote: env("DASHSCOPE_QUOTA_NOTE", "unknown; inspect provider account"), PricingNote: env("DASHSCOPE_PRICING_NOTE", "token-based; account-effective price/quota must be captured at execution"), PrivacyNote: "Recorded corpus audio and generated response content are sent to Alibaba Cloud Model Studio for this lane."}}}
	missing := missingNames(map[string]string{"DASHSCOPE_REALTIME_KEY": key, "DASHSCOPE_WORKSPACE_ID": workspace})
	if len(missing) > 0 {
		lane.Status = "insufficient_evidence"
		lane.Blockers = []string{"missing GitHub/provider credentials: " + strings.Join(missing, ", ")}
		return lane
	}
	endpoint := strings.TrimSpace(os.Getenv("DASHSCOPE_REALTIME_URL"))
	if endpoint == "" {
		endpoint = qwenEndpoint(region, workspace)
	}
	factory := func() (speech.NativeRealtimeProvider, error) {
		return speech.NewQwenRealtime(speech.QwenRealtimeConfig{URL: endpoint, Model: model, APIKey: key, WorkspaceID: workspace, Voice: voice, TurnDetection: "manual", InputTranscriptionModel: "qwen3-asr-flash-realtime"})
	}
	for _, c := range corpus.Cases {
		provider, err := factory()
		if err != nil {
			lane.Cases = append(lane.Cases, errorCase(c, err))
			continue
		}
		lane.Cases = append(lane.Cases, runRealtimeCase(c, provider, pace))
	}
	lane.Summary = speechbench.Summarize(lane.Cases)
	lane.Cancellation = runRealtimeCancel(factory, firstCase(corpus, "en"), pace)
	lane.Reconnect = runRealtimeReconnect(factory)
	finishLane(&lane)
	return lane
}

func runCascadeCase(c speechbench.CorpusCase, asr speech.StreamingASRProvider, tts speech.StreamingTTSProvider, pace bool, voice string) speechbench.CaseResult {
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
	if err := pushPCM(ctx, stream, pcm, pace); err != nil {
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

func runRealtimeCase(c speechbench.CorpusCase, provider speech.NativeRealtimeProvider, pace bool) speechbench.CaseResult {
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

	received := make(chan realtimeReceive, 32)
	go func() {
		for {
			event, receiveErr := session.Receive(ctx)
			select {
			case received <- realtimeReceive{event: event, err: receiveErr}:
			case <-ctx.Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()

	inputStarted := time.Now()
	if err := appendRealtimePCM(ctx, session, pcm, pace); err != nil {
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
	var asrFinalAt time.Time
	var firstAudioAt time.Time
	var audioDoneAt time.Time
	var responseDoneAt time.Time
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
					v := ms(now.Sub(inputStarted))
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
		result.Error = "Qwen Realtime response completed without input_audio_transcription.completed"
		return result
	}
	if firstAudioAt.IsZero() {
		result.Error = "Qwen Realtime completed without audio"
		return result
	}
	result.Transcript = transcript
	result.ASRFirstPartialMS = firstInput
	result.ASRFinalMS = max0(ms(asrFinalAt.Sub(commitAt)))
	result.WER = speechbench.WER(c.Reference, transcript)
	result.CER = speechbench.CER(c.Reference, transcript)
	result.TTSFirstAudioMS = max0(ms(firstAudioAt.Sub(commitAt)))
	if !audioDoneAt.IsZero() {
		result.TTSTotalMS = max0(ms(audioDoneAt.Sub(firstAudioAt)))
	}
	result.TTSPCMBytes = bytes
	result.TTSChunks = chunks
	result.SpeechToFirstAudioMS = c.DurationMS + result.TTSFirstAudioMS
	turn := ms(responseDoneAt.Sub(inputStarted))
	result.TurnE2EMS = &turn
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

func runRealtimeCancel(factory func() (speech.NativeRealtimeProvider, error), c speechbench.CorpusCase, pace bool) *speechbench.CancellationResult {
	res := &speechbench.CancellationResult{Attempted: true}
	provider, err := factory()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	pcm, err := verifiedPCM(c)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s, err := provider.Connect(ctx)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer s.Close()
	if err := appendRealtimePCM(ctx, s, pcm, pace); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := s.CommitAudio(ctx); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := s.CreateResponse(ctx); err != nil {
		res.Error = err.Error()
		return res
	}
	first := false
	cancelAt := time.Time{}
	for {
		e, err := s.Receive(ctx)
		if err != nil {
			if first && ctx.Err() == nil {
				res.Error = err.Error()
			}
			return res
		}
		if len(e.AudioPCM) > 0 {
			if !first {
				first = true
				cancelAt = time.Now()
				if err := s.CancelResponse(ctx); err != nil {
					res.Error = err.Error()
					return res
				}
				continue
			}
			res.StaleAudioChunks++
		}
		if first && e.ResponseDone {
			res.CancelLatencyMS = ms(time.Since(cancelAt))
			res.Cancelled = res.StaleAudioChunks == 0
			res.Evidence = "response.cancel observed through native realtime session until response.done"
			return res
		}
	}
}

func runASRReconnect(factory func(speechbench.CorpusCase) (speech.StreamingASRProvider, error), c speechbench.CorpusCase, pace bool) *speechbench.ReconnectResult {
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
			err = pushPCM(ctx, stream, pcm, pace)
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

func runRealtimeReconnect(factory func() (speech.NativeRealtimeProvider, error)) *speechbench.ReconnectResult {
	res := &speechbench.ReconnectResult{Attempted: true}
	for i := 0; i < 2; i++ {
		p, err := factory()
		if err != nil {
			res.Error = err.Error()
			return res
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		s, err := p.Connect(ctx)
		if err == nil {
			err = s.Close()
		}
		cancel()
		if err != nil {
			res.Error = fmt.Sprintf("connect %d: %v", i+1, err)
			return res
		}
	}
	res.Passed = true
	res.Evidence = "two independent authenticated realtime websocket sessions opened and closed"
	return res
}

func finishLane(lane *speechbench.LaneResult) {
	failures := 0
	for _, c := range lane.Cases {
		if c.Error != "" {
			failures++
			lane.Failures = append(lane.Failures, c.ID+": "+c.Error)
		}
	}
	if failures > 0 {
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

func pushPCM(ctx context.Context, s speech.ASRStream, pcm []byte, pace bool) error {
	const chunk = 1280
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := s.Push(ctx, pcm[off:end]); err != nil {
			return err
		}
		if pace {
			if err := sleepContext(ctx, time.Duration(float64(time.Second)*float64(end-off)/(inputRate*2))); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendRealtimePCM(ctx context.Context, s speech.NativeRealtimeSession, pcm []byte, pace bool) error {
	const chunk = 3200
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := s.AppendAudio(ctx, pcm[off:end]); err != nil {
			return err
		}
		if pace {
			if err := sleepContext(ctx, time.Duration(float64(time.Second)*float64(end-off)/(inputRate*2))); err != nil {
				return err
			}
		}
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func xunfeiLanguage(language string) (string, string, string) {
	switch language {
	case "en":
		return "en_us", env("XUNFEI_ASR_URL", "wss://iat-api.xfyun.cn/v2/iat"), ""
	case "vi":
		lang := strings.TrimSpace(os.Getenv("XUNFEI_ASR_VI_LANGUAGE"))
		if lang == "" {
			return "", "", "Xunfei Vietnamese account-authorized language code is not configured"
		}
		return lang, env("XUNFEI_ASR_VI_URL", "wss://iat-niche-api.xfyun.cn/v2/iat"), ""
	case "mixed":
		lang := strings.TrimSpace(os.Getenv("XUNFEI_ASR_MIXED_LANGUAGE"))
		if lang == "" {
			return "", "", "Xunfei mixed/code-switch language configuration is not verified for this account"
		}
		return lang, env("XUNFEI_ASR_MIXED_URL", "wss://iat-niche-api.xfyun.cn/v2/iat"), ""
	}
	return "", "", "unsupported language"
}

func qwenEndpoint(region, workspace string) string {
	host := "cn-beijing.maas.aliyuncs.com"
	if region == "ap-southeast-1" {
		host = "ap-southeast-1.maas.aliyuncs.com"
	}
	return fmt.Sprintf("wss://%s.%s/api-ws/v1/realtime", workspace, host)
}

func parseLanes(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func missingNames(values map[string]string) []string {
	var out []string
	for k, v := range values {
		if strings.TrimSpace(v) == "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
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

func max0(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "bench-speech:", err)
	os.Exit(2)
}
