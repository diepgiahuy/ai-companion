package speech

import (
	"context"
	"errors"

	"companion-server/internal/pipeline"
)

// PipelineAdapter binds a speech provider to the stable realtime pipeline
// without leaking provider SDK types into server/session code.
type PipelineAdapter struct {
	ASRProvider StreamingASRProvider
	TTSProvider StreamingTTSProvider
	ASRFormat   AudioFormat
	TTSFormat   AudioFormat
	Locale      string
	Voice       string
}

func (a PipelineAdapter) Validate() error {
	if a.ASRProvider == nil {
		return errors.New("speech ASR provider is required")
	}
	if a.TTSProvider == nil {
		return errors.New("speech TTS provider is required")
	}
	if err := a.ASRFormat.Validate(); err != nil {
		return err
	}
	return a.TTSFormat.Validate()
}

func (a PipelineAdapter) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	stream, err := a.ASRProvider.StartASR(ctx, ASRRequest{Format: a.ASRFormat, Locale: a.Locale}, func(TranscriptEvent) error { return nil })
	if err != nil {
		return "", err
	}
	defer stream.Close()
	if err := stream.Push(ctx, pcm); err != nil {
		return "", err
	}
	if err := stream.CloseInput(ctx); err != nil {
		return "", err
	}
	return stream.Wait(ctx)
}

func (a PipelineAdapter) TranscribeStream(ctx context.Context, pcm <-chan []byte, emit func(pipeline.ASRPartial) error) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	if emit == nil {
		return "", errors.New("speech ASR emit callback is required")
	}
	stream, err := a.ASRProvider.StartASR(ctx, ASRRequest{Format: a.ASRFormat, Locale: a.Locale}, func(event TranscriptEvent) error {
		return emit(pipeline.ASRPartial{
			Text:       event.Text,
			Final:      event.Final,
			Confidence: event.Confidence,
			Stable:     event.Stable,
		})
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case chunk, ok := <-pcm:
			if !ok {
				if err := stream.CloseInput(ctx); err != nil {
					return "", err
				}
				return stream.Wait(ctx)
			}
			if len(chunk) == 0 {
				continue
			}
			if err := stream.Push(ctx, chunk); err != nil {
				return "", err
			}
		}
	}
}

func (a PipelineAdapter) Synthesize(ctx context.Context, text string, emit func([]byte) error) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if emit == nil {
		return errors.New("speech TTS emit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.TTSProvider.Synthesize(ctx, TTSRequest{
		Text:   text,
		Voice:  a.Voice,
		Locale: a.Locale,
		Format: a.TTSFormat,
	}, func(event AudioEvent) error {
		if len(event.PCM) == 0 {
			return nil
		}
		return emit(event.PCM)
	})
}

func (a PipelineAdapter) Close() error {
	var errs []error
	if closer, ok := a.ASRProvider.(interface{ Close() error }); ok && closer != nil {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if closer, ok := a.TTSProvider.(interface{ Close() error }); ok && closer != nil && any(a.TTSProvider) != any(a.ASRProvider) {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var _ pipeline.ASR = PipelineAdapter{}
var _ pipeline.StreamingASR = PipelineAdapter{}
var _ pipeline.TTS = PipelineAdapter{}
var _ interface{ Close() error } = PipelineAdapter{}

