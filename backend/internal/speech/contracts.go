package speech

import (
	"context"
	"errors"
	"fmt"
)

// AudioFormat is the provider-neutral PCM boundary used between Companion and
// speech adapters. Product transport remains Opus; providers never see device
// protocol envelopes.
type AudioFormat struct {
	SampleRate int
	Channels   int
}

func (f AudioFormat) Validate() error {
	if f.SampleRate <= 0 {
		return errors.New("speech audio sample rate must be positive")
	}
	if f.Channels != 1 {
		return fmt.Errorf("speech audio channels=%d: only mono is supported", f.Channels)
	}
	return nil
}

// TranscriptEvent is one normalized ASR update. Final must be emitted exactly
// once for a successful stream. Confidence/Stability are optional and must not
// become provider-specific correctness requirements.
type TranscriptEvent struct {
	Text       string
	Final      bool
	Confidence float64
	Stable     bool
}

// ASRRequest fixes the audio/language contract before a stream is created.
// Locale may be empty when an adapter supports automatic language detection.
type ASRRequest struct {
	Format AudioFormat
	Locale string
}

// ASRStream owns one provider recognition session. Push must respect ctx and
// apply bounded provider backpressure rather than accumulating unbounded audio.
type ASRStream interface {
	Push(ctx context.Context, pcm []byte) error
	CloseInput(ctx context.Context) error
	Wait(ctx context.Context) (string, error)
	Close() error
}

// StreamingASRProvider creates one recognition stream per Companion turn.
// Provider SDK types stay behind this interface.
type StreamingASRProvider interface {
	StartASR(ctx context.Context, request ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error)
}

// TTSRequest describes one synthesis segment. Providers must emit PCM in the
// requested format or return a deterministic unsupported-format error.
type TTSRequest struct {
	Text     string
	Voice    string
	Locale   string
	Format   AudioFormat
	TurnID   string
	Sentence int
}

// AudioEvent is normalized streaming TTS output. Final is a lifecycle marker;
// it may carry no PCM. PCM ownership transfers to the caller for the duration
// of emit only, so callers copy data they need after the callback returns.
type AudioEvent struct {
	PCM   []byte
	Final bool
}

// StreamingTTSProvider streams first audio before the full utterance has to be
// buffered. Implementations must stop callbacks promptly after ctx cancellation.
type StreamingTTSProvider interface {
	Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error
}

// Capabilities are immutable adapter facts used by composition/benchmarking;
// they are not a runtime fallback selector.
type Capabilities struct {
	Name              string
	StreamingASR      bool
	StreamingTTS      bool
	Locales           []string
	ASRSampleRates    []int
	TTSSampleRates    []int
	RequiresNetwork   bool
	RequiresCredential bool
}

// Provider is the complete speech boundary for a candidate that supplies both
// recognition and synthesis. Benchmark tooling may evaluate split providers,
// but Product v1 promotes one configured ASR and one configured TTS path.
type Provider interface {
	StreamingASRProvider
	StreamingTTSProvider
	Capabilities() Capabilities
	Close() error
}
