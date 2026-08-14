package speech

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	defaultEdgeTTSMaxMP3Bytes = 16 * 1024 * 1024
	defaultEdgeTTSMaxPCMBytes = 32 * 1024 * 1024
	defaultEdgePCMChunkBytes  = 2880 // 60 ms of 24 kHz mono PCM16
)

// EdgeTTSRunner is injectable so unit tests verify the exact process boundary
// without network access or requiring Python/ffmpeg in the Go test image.
type EdgeTTSRunner interface {
	Run(ctx context.Context, command string, args []string, stdin []byte, maxOutputBytes int) ([]byte, error)
}

type execEdgeTTSRunner struct{}

func (execEdgeTTSRunner) Run(ctx context.Context, command string, args []string, stdin []byte, maxOutputBytes int) ([]byte, error) {
	if maxOutputBytes <= 0 {
		return nil, errors.New("process output limit must be positive")
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{buffer: &stdout, remaining: maxOutputBytes}
	cmd.Stderr = &limitedBuffer{buffer: &stderr, remaining: 64 * 1024}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	buffer    *bytes.Buffer
	remaining int
	overflow  bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.overflow {
		return len(p), nil
	}
	if len(p) > w.remaining {
		if w.remaining > 0 {
			_, _ = w.buffer.Write(p[:w.remaining])
		}
		w.remaining = 0
		w.overflow = true
		return len(p), errors.New("process output exceeded configured limit")
	}
	n, err := w.buffer.Write(p)
	w.remaining -= n
	return n, err
}

type EdgeTTSConfig struct {
	Command       string
	FFmpegCommand string
	Voice         string
	Rate          string
	Volume        string
	Pitch         string
	Runner        EdgeTTSRunner
	MaxMP3Bytes   int
	MaxPCMBytes   int
	PCMChunkBytes int
}

func (c EdgeTTSConfig) normalized() (EdgeTTSConfig, error) {
	if strings.TrimSpace(c.Command) == "" {
		c.Command = "edge-tts"
	}
	if strings.TrimSpace(c.FFmpegCommand) == "" {
		c.FFmpegCommand = "ffmpeg"
	}
	if strings.TrimSpace(c.Voice) == "" {
		c.Voice = "vi-VN-HoaiMyNeural"
	}
	if c.Rate == "" {
		c.Rate = "+0%"
	}
	if c.Volume == "" {
		c.Volume = "+0%"
	}
	if c.Pitch == "" {
		c.Pitch = "+0Hz"
	}
	if c.Runner == nil {
		c.Runner = execEdgeTTSRunner{}
	}
	if c.MaxMP3Bytes <= 0 {
		c.MaxMP3Bytes = defaultEdgeTTSMaxMP3Bytes
	}
	if c.MaxPCMBytes <= 0 {
		c.MaxPCMBytes = defaultEdgeTTSMaxPCMBytes
	}
	if c.PCMChunkBytes <= 0 {
		c.PCMChunkBytes = defaultEdgePCMChunkBytes
	}
	if c.PCMChunkBytes%2 != 0 {
		return c, errors.New("EdgeTTS PCM chunk size must align to 16-bit samples")
	}
	return c, nil
}

type EdgeTTSProvider struct {
	config EdgeTTSConfig
}

func NewEdgeTTS(config EdgeTTSConfig) (*EdgeTTSProvider, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &EdgeTTSProvider{config: normalized}, nil
}

func (p *EdgeTTSProvider) Synthesize(ctx context.Context, request TTSRequest, emit func(AudioEvent) error) error {
	if p == nil {
		return errors.New("EdgeTTS provider is nil")
	}
	if emit == nil {
		return errors.New("EdgeTTS emit callback is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Format.Validate(); err != nil {
		return err
	}
	if request.Format.SampleRate != 24000 || request.Format.Channels != 1 {
		return fmt.Errorf("EdgeTTS reference path requires 24000 Hz mono PCM; got %d Hz/%d channels", request.Format.SampleRate, request.Format.Channels)
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return errors.New("EdgeTTS text is required")
	}
	voice := strings.TrimSpace(request.Voice)
	if voice == "" {
		voice = p.config.Voice
	}

	mp3, err := p.config.Runner.Run(ctx, p.config.Command, []string{
		"--file", "-",
		"--voice", voice,
		"--rate=" + p.config.Rate,
		"--volume=" + p.config.Volume,
		"--pitch=" + p.config.Pitch,
		"--write-media", "-",
	}, []byte(text), p.config.MaxMP3Bytes)
	if err != nil {
		return fmt.Errorf("EdgeTTS synthesize MP3: %w", err)
	}
	if len(mp3) == 0 {
		return errors.New("EdgeTTS completed without audio")
	}

	pcm, err := p.config.Runner.Run(ctx, p.config.FFmpegCommand, []string{
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "1", "-ar", "24000",
		"pipe:1",
	}, mp3, p.config.MaxPCMBytes)
	if err != nil {
		return fmt.Errorf("decode EdgeTTS MP3: %w", err)
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return fmt.Errorf("EdgeTTS decoder returned invalid PCM length %d", len(pcm))
	}

	for offset := 0; offset < len(pcm); offset += p.config.PCMChunkBytes {
		end := offset + p.config.PCMChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(AudioEvent{PCM: append([]byte(nil), pcm[offset:end]...)}); err != nil {
			return err
		}
	}
	return emit(AudioEvent{Final: true})
}

var _ StreamingTTSProvider = (*EdgeTTSProvider)(nil)
