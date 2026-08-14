package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultFunASRMaxPCMBytes = 4 * 1024 * 1024

// FunASRConfig connects Companion to FunASR's official OpenAI-compatible local
// transcription endpoint. BaseURL may be either http://127.0.0.1:8000 or the
// same URL with /v1 appended. The model is deliberately explicit so Companion
// does not silently fall back to SenseVoice (which does not cover Vietnamese).
type FunASRConfig struct {
	BaseURL     string
	Model       string
	HTTPClient  *http.Client
	MaxPCMBytes int
}

type FunASR struct {
	baseURL     string
	model       string
	client      *http.Client
	maxPCMBytes int
}

func NewFunASR(config FunASRConfig) (*FunASR, error) {
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if base == "" {
		return nil, errors.New("FunASR base URL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid FunASR base URL %q", config.BaseURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported FunASR URL scheme %q", parsed.Scheme)
	}
	model := strings.TrimSpace(config.Model)
	if model == "" {
		return nil, errors.New("FunASR model is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	maxPCMBytes := config.MaxPCMBytes
	if maxPCMBytes <= 0 {
		maxPCMBytes = defaultFunASRMaxPCMBytes
	}
	return &FunASR{baseURL: base, model: model, client: client, maxPCMBytes: maxPCMBytes}, nil
}

func (p *FunASR) StartASR(ctx context.Context, request ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := request.Format.Validate(); err != nil {
		return nil, err
	}
	if emit == nil {
		return nil, errors.New("FunASR transcript callback is required")
	}
	return &funASRStream{provider: p, request: request, emit: emit}, nil
}

type funASRStream struct {
	provider *FunASR
	request  ASRRequest
	emit     func(TranscriptEvent) error

	mu      sync.Mutex
	pcm     []byte
	closed  bool
	waited  bool
	stopped bool
}

func (s *funASRStream) Push(ctx context.Context, pcm []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(pcm) == 0 {
		return nil
	}
	if len(pcm)%2 != 0 {
		return errors.New("FunASR PCM16 input must contain whole 16-bit samples")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("FunASR stream is closed")
	}
	if s.closed {
		return errors.New("FunASR input is already closed")
	}
	if len(s.pcm)+len(pcm) > s.provider.maxPCMBytes {
		return fmt.Errorf("FunASR PCM input exceeds %d-byte turn limit", s.provider.maxPCMBytes)
	}
	s.pcm = append(s.pcm, pcm...)
	return nil
}

func (s *funASRStream) CloseInput(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("FunASR stream is closed")
	}
	s.closed = true
	return nil
}

func (s *funASRStream) Wait(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return "", errors.New("FunASR stream is closed")
	}
	if !s.closed {
		s.mu.Unlock()
		return "", errors.New("FunASR input must be closed before waiting")
	}
	if s.waited {
		s.mu.Unlock()
		return "", errors.New("FunASR stream Wait may be called only once")
	}
	s.waited = true
	pcm := append([]byte(nil), s.pcm...)
	s.mu.Unlock()

	text, err := s.provider.transcribe(ctx, s.request, pcm)
	if err != nil {
		return "", err
	}
	if err := s.emit(TranscriptEvent{Text: text, Final: true, Stable: true}); err != nil {
		return "", err
	}
	return text, nil
}

func (s *funASRStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.pcm = nil
	return nil
}

func (p *FunASR) transcribe(ctx context.Context, request ASRRequest, pcm []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "companion-turn.wav")
	if err != nil {
		return "", err
	}
	if _, err := file.Write(pcm16WAV(request.Format, pcm)); err != nil {
		return "", err
	}
	if err := writer.WriteField("model", p.model); err != nil {
		return "", err
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if locale := strings.TrimSpace(request.Locale); locale != "" {
		// FunASR's OpenAI-compatible endpoint accepts an optional language field.
		// Keep the Companion locale intact; model-specific normalization belongs
		// in the local FunASR deployment/config, not the device protocol.
		if err := writer.WriteField("language", locale); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	endpoint := p.baseURL
	if strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/audio/transcriptions"
	} else {
		endpoint += "/v1/audio/transcriptions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("FunASR transcription failed: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	var result struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return "", fmt.Errorf("decode FunASR response: %w", err)
	}
	return strings.TrimSpace(result.Text), nil
}

func pcm16WAV(format AudioFormat, pcm []byte) []byte {
	const headerSize = 44
	out := make([]byte, headerSize+len(pcm))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+len(pcm)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(out[22:24], uint16(format.Channels))
	binary.LittleEndian.PutUint32(out[24:28], uint32(format.SampleRate))
	byteRate := format.SampleRate * format.Channels * 2
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], uint16(format.Channels*2))
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}

var _ StreamingASRProvider = (*FunASR)(nil)
