package speech

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"companion-server/internal/pipeline"
)

type fakeASRProvider struct {
	startErr error
	stream   *fakeASRStream
}

type fakeASRStream struct {
	emit          func(TranscriptEvent) error
	chunks        int
	closed        bool
	pushErr       error
	closeInputErr error
	waitErr       error
	waitResult    string
}

func (p fakeASRProvider) StartASR(_ context.Context, _ ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error) {
	if p.startErr != nil {
		return nil, p.startErr
	}
	if p.stream != nil {
		p.stream.emit = emit
		return p.stream, nil
	}
	return &fakeASRStream{emit: emit, waitResult: "xin chao"}, nil
}

func (s *fakeASRStream) Push(ctx context.Context, pcm []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.pushErr != nil {
		return s.pushErr
	}
	if len(pcm) > 0 {
		s.chunks++
		return s.emit(TranscriptEvent{Text: "xin", Stable: true})
	}
	return nil
}

func (s *fakeASRStream) CloseInput(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.closeInputErr != nil {
		return s.closeInputErr
	}
	return s.emit(TranscriptEvent{Text: "xin chao", Final: true, Stable: true})
}

func (s *fakeASRStream) Wait(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.waitErr != nil {
		return "", s.waitErr
	}
	if s.chunks == 0 && s.waitResult == "" {
		return "", errors.New("no audio")
	}
	if s.waitResult != "" {
		return s.waitResult, nil
	}
	return "xin chao", nil
}

func (s *fakeASRStream) Close() error {
	s.closed = true
	return nil
}

type fakeClosableASRProvider struct {
	fakeASRProvider
	closed bool
}

func (p *fakeClosableASRProvider) Close() error {
	p.closed = true
	return nil
}

type fakeTTSProvider struct {
	synthErr error
	frames   [][]byte
}

func (p fakeTTSProvider) Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error {
	if p.synthErr != nil {
		return p.synthErr
	}
	if request.Text == "" {
		return errors.New("empty text")
	}
	frames := p.frames
	if frames == nil {
		frames = [][]byte{{1, 2}, {3, 4}}
	}
	for _, frame := range frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(AudioEvent{PCM: frame}); err != nil {
			return err
		}
	}
	return emit(AudioEvent{Final: true})
}

type fakeClosableTTSProvider struct {
	fakeTTSProvider
	closed bool
}

func (p *fakeClosableTTSProvider) Close() error {
	p.closed = true
	return nil
}

func testAdapter() PipelineAdapter {
	return PipelineAdapter{
		ASRProvider: fakeASRProvider{},
		TTSProvider: fakeTTSProvider{},
		ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
		Locale:      "vi-VN",
		Voice:       "test",
	}
}

func TestPipelineAdapterStreamsASRPartialsAndFinal(t *testing.T) {
	a := testAdapter()
	chunks := make(chan []byte, 2)
	chunks <- []byte{1, 2}
	chunks <- []byte{3, 4}
	close(chunks)

	var events []pipeline.ASRPartial
	text, err := a.TranscribeStream(context.Background(), chunks, func(event pipeline.ASRPartial) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "xin chao" {
		t.Fatalf("text=%q", text)
	}
	if len(events) != 3 || !events[len(events)-1].Final {
		t.Fatalf("events=%+v", events)
	}
}

func TestPipelineAdapterTranscribeSingleShot(t *testing.T) {
	a := testAdapter()
	text, err := a.Transcribe(context.Background(), []byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if text != "xin chao" {
		t.Fatalf("text=%q, want 'xin chao'", text)
	}
}

func TestPipelineAdapterTranscribeErrors(t *testing.T) {
	stream := &fakeASRStream{pushErr: errors.New("push failed")}
	a := PipelineAdapter{
		ASRProvider: fakeASRProvider{stream: stream},
		TTSProvider: fakeTTSProvider{},
		ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
	}
	if _, err := a.Transcribe(context.Background(), []byte{1, 2}); err == nil || !strings.Contains(err.Error(), "push failed") {
		t.Fatalf("expected push failed error, got %v", err)
	}
	if !stream.closed {
		t.Fatal("expected stream to be closed after error")
	}
}

func TestPipelineAdapterTranscribeStreamPushAndCloseErrors(t *testing.T) {
	t.Run("push error", func(t *testing.T) {
		stream := &fakeASRStream{pushErr: errors.New("push broken")}
		a := PipelineAdapter{
			ASRProvider: fakeASRProvider{stream: stream},
			TTSProvider: fakeTTSProvider{},
			ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
			TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
		}
		chunks := make(chan []byte, 1)
		chunks <- []byte{1, 2}
		close(chunks)
		if _, err := a.TranscribeStream(context.Background(), chunks, func(pipeline.ASRPartial) error { return nil }); err == nil || !strings.Contains(err.Error(), "push broken") {
			t.Fatalf("got %v, want push broken", err)
		}
		if !stream.closed {
			t.Fatal("stream should be closed on push error")
		}
	})

	t.Run("close input error", func(t *testing.T) {
		stream := &fakeASRStream{closeInputErr: errors.New("close input broken")}
		a := PipelineAdapter{
			ASRProvider: fakeASRProvider{stream: stream},
			TTSProvider: fakeTTSProvider{},
			ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
			TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
		}
		chunks := make(chan []byte)
		close(chunks)
		if _, err := a.TranscribeStream(context.Background(), chunks, func(pipeline.ASRPartial) error { return nil }); err == nil || !strings.Contains(err.Error(), "close input broken") {
			t.Fatalf("got %v, want close input broken", err)
		}
		if !stream.closed {
			t.Fatal("stream should be closed on closeInput error")
		}
	})
}

func TestPipelineAdapterStreamsTTSFrames(t *testing.T) {
	a := testAdapter()
	var got [][]byte
	if err := a.Synthesize(context.Background(), "hello", func(frame []byte) error {
		got = append(got, append([]byte(nil), frame...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{{1, 2}, {3, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frames=%v want=%v", got, want)
	}
}

func TestPipelineAdapterSynthesizeErrors(t *testing.T) {
	a := testAdapter()

	// Canceled context before synthesis
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Synthesize(ctx, "hello", func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}

	// Missing emit callback
	if err := a.Synthesize(context.Background(), "hello", nil); err == nil {
		t.Fatal("expected error on nil emit callback")
	}

	// Emit callback error propagation
	emitErr := errors.New("emit failed")
	if err := a.Synthesize(context.Background(), "hello", func([]byte) error { return emitErr }); !errors.Is(err, emitErr) {
		t.Fatalf("got %v, want emit failed", err)
	}
}

func TestPipelineAdapterCancellationStopsBeforeProviderWork(t *testing.T) {
	a := testAdapter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chunks := make(chan []byte, 1)
	chunks <- []byte{1}
	close(chunks)
	if _, err := a.TranscribeStream(ctx, chunks, func(pipeline.ASRPartial) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context canceled", err)
	}
}

func TestPipelineAdapterCloseClosesUnderlyingProviders(t *testing.T) {
	closableASR := &fakeClosableASRProvider{}
	closableTTS := &fakeClosableTTSProvider{}
	a := PipelineAdapter{
		ASRProvider: closableASR,
		TTSProvider: closableTTS,
		ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
	}
	if err := a.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if !closableASR.closed || !closableTTS.closed {
		t.Fatalf("closable providers not closed: ASR=%v TTS=%v", closableASR.closed, closableTTS.closed)
	}
}

func TestPipelineAdapterValidation(t *testing.T) {
	// Missing ASR
	a1 := PipelineAdapter{
		TTSProvider: fakeTTSProvider{},
		ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
	}
	if err := a1.Validate(); err == nil {
		t.Fatal("expected error for missing ASR")
	}

	// Missing TTS
	a2 := PipelineAdapter{
		ASRProvider: fakeASRProvider{},
		ASRFormat:   AudioFormat{SampleRate: 16000, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
	}
	if err := a2.Validate(); err == nil {
		t.Fatal("expected error for missing TTS")
	}

	// Invalid format in Transcribe
	a3 := PipelineAdapter{
		ASRProvider: fakeASRProvider{},
		TTSProvider: fakeTTSProvider{},
		ASRFormat:   AudioFormat{SampleRate: 0, Channels: 1},
		TTSFormat:   AudioFormat{SampleRate: 24000, Channels: 1},
	}
	if _, err := a3.Transcribe(context.Background(), []byte{1, 2}); err == nil {
		t.Fatal("expected error on invalid format")
	}
	if _, err := a3.TranscribeStream(context.Background(), nil, func(pipeline.ASRPartial) error { return nil }); err == nil {
		t.Fatal("expected error on invalid format")
	}

	// Nil emit callback
	if _, err := testAdapter().TranscribeStream(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error on nil emit in TranscribeStream")
	}
}

func TestAudioFormatRejectsUnsupportedShape(t *testing.T) {
	if err := (AudioFormat{SampleRate: 16000, Channels: 2}).Validate(); err == nil {
		t.Fatal("stereo format unexpectedly accepted")
	}
	if err := (AudioFormat{SampleRate: 0, Channels: 1}).Validate(); err == nil {
		t.Fatal("zero sample rate unexpectedly accepted")
	}
}
