package speech

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"companion-server/internal/pipeline"
)

type fakeASRProvider struct{}

type fakeASRStream struct {
	emit   func(TranscriptEvent) error
	chunks int
	closed bool
}

func (fakeASRProvider) StartASR(_ context.Context, _ ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error) {
	return &fakeASRStream{emit: emit}, nil
}

func (s *fakeASRStream) Push(ctx context.Context, pcm []byte) error {
	if err := ctx.Err(); err != nil {
		return err
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
	return s.emit(TranscriptEvent{Text: "xin chao", Final: true, Stable: true})
}

func (s *fakeASRStream) Wait(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.chunks == 0 {
		return "", errors.New("no audio")
	}
	return "xin chao", nil
}

func (s *fakeASRStream) Close() error {
	s.closed = true
	return nil
}

type fakeTTSProvider struct{}

func (fakeTTSProvider) Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error {
	if request.Text == "" {
		return errors.New("empty text")
	}
	for _, frame := range [][]byte{{1, 2}, {3, 4}} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(AudioEvent{PCM: frame}); err != nil {
			return err
		}
	}
	return emit(AudioEvent{Final: true})
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

func TestAudioFormatRejectsUnsupportedShape(t *testing.T) {
	if err := (AudioFormat{SampleRate: 16000, Channels: 2}).Validate(); err == nil {
		t.Fatal("stereo format unexpectedly accepted")
	}
	if err := (AudioFormat{SampleRate: 0, Channels: 1}).Validate(); err == nil {
		t.Fatal("zero sample rate unexpectedly accepted")
	}
}
