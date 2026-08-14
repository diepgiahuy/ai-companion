package speech

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// FunASRConfig targets the official FunASR realtime WebSocket service. The
// default 2-pass configuration emits low-latency online text and corrected
// offline text at sentence/final boundaries.
type FunASRConfig struct {
	URL                  string
	Mode                 string
	ChunkSize            [3]int
	ChunkInterval        int
	EncoderChunkLookBack int
	DecoderChunkLookBack int
	UseITN               bool
	Hotwords             string
	HTTPClient           *http.Client
}

func (c FunASRConfig) normalized() (FunASRConfig, error) {
	c.URL = strings.TrimSpace(c.URL)
	if c.URL == "" {
		return c, errors.New("FunASR URL is required")
	}
	if !strings.HasPrefix(c.URL, "ws://") && !strings.HasPrefix(c.URL, "wss://") {
		return c, errors.New("FunASR URL must use ws:// or wss://")
	}
	if c.Mode == "" {
		c.Mode = "2pass"
	}
	if c.Mode != "online" && c.Mode != "2pass" {
		return c, fmt.Errorf("FunASR mode %q is unsupported; use online or 2pass", c.Mode)
	}
	if c.ChunkSize == [3]int{} {
		c.ChunkSize = [3]int{5, 10, 5}
	}
	if c.ChunkInterval <= 0 {
		c.ChunkInterval = 10
	}
	if c.EncoderChunkLookBack == 0 {
		c.EncoderChunkLookBack = 4
	}
	return c, nil
}

type FunASRProvider struct {
	config FunASRConfig
}

func NewFunASR(config FunASRConfig) (*FunASRProvider, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &FunASRProvider{config: normalized}, nil
}

func (p *FunASRProvider) StartASR(ctx context.Context, request ASRRequest, emit func(TranscriptEvent) error) (ASRStream, error) {
	if p == nil {
		return nil, errors.New("FunASR provider is nil")
	}
	if emit == nil {
		return nil, errors.New("FunASR emit callback is required")
	}
	if err := request.Format.Validate(); err != nil {
		return nil, err
	}
	if request.Format.SampleRate != 16000 {
		return nil, fmt.Errorf("FunASR requires 16000 Hz PCM; got %d", request.Format.SampleRate)
	}

	dialOptions := &websocket.DialOptions{Subprotocols: []string{"binary"}}
	if p.config.HTTPClient != nil {
		dialOptions.HTTPClient = p.config.HTTPClient
	}
	conn, _, err := websocket.Dial(ctx, p.config.URL, dialOptions)
	if err != nil {
		return nil, fmt.Errorf("dial FunASR: %w", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &funASRStream{
		conn:   conn,
		cancel: cancel,
		emit:   emit,
		done:   make(chan struct{}),
	}
	initial := map[string]any{
		"mode":                    p.config.Mode,
		"chunk_size":              []int{p.config.ChunkSize[0], p.config.ChunkSize[1], p.config.ChunkSize[2]},
		"chunk_interval":          p.config.ChunkInterval,
		"encoder_chunk_look_back": p.config.EncoderChunkLookBack,
		"decoder_chunk_look_back": p.config.DecoderChunkLookBack,
		"audio_fs":                request.Format.SampleRate,
		"wav_name":                "companion",
		"wav_format":              "pcm",
		"is_speaking":             true,
		"hotwords":                p.config.Hotwords,
		"itn":                     p.config.UseITN,
	}
	payload, err := json.Marshal(initial)
	if err != nil {
		cancel()
		conn.CloseNow()
		return nil, err
	}
	if err := conn.Write(streamCtx, websocket.MessageText, payload); err != nil {
		cancel()
		conn.CloseNow()
		return nil, fmt.Errorf("initialize FunASR stream: %w", err)
	}
	go stream.readLoop(streamCtx)
	return stream, nil
}

type funASRMessage struct {
	Mode      string `json:"mode"`
	Text      string `json:"text"`
	IsFinal   bool   `json:"is_final"`
	IsEnd     bool   `json:"is_end"`
	Error     string `json:"error"`
	Event     string `json:"event"`
	Partial   string `json:"partial"`
	Sentences []struct {
		Text string `json:"text"`
	} `json:"sentences"`
}

type funASRStream struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	emit   func(TranscriptEvent) error

	writeMu sync.Mutex
	mu      sync.Mutex
	text    string
	err     error
	closed  bool
	done    chan struct{}
	once    sync.Once
}

func (s *funASRStream) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *funASRStream) readLoop(ctx context.Context) {
	for {
		kind, raw, err := s.conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				s.finish(ctx.Err())
			} else {
				s.finish(fmt.Errorf("read FunASR: %w", err))
			}
			return
		}
		if kind != websocket.MessageText {
			continue
		}
		var message funASRMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			s.finish(fmt.Errorf("decode FunASR response: %w", err))
			return
		}
		if message.Error != "" {
			s.finish(fmt.Errorf("FunASR error: %s", message.Error))
			return
		}
		if message.Event != "" {
			if message.Event == "stopped" && !message.IsFinal {
				s.finish(nil)
				return
			}
			continue
		}

		text := strings.TrimSpace(message.Text)
		if text == "" && strings.TrimSpace(message.Partial) != "" {
			text = strings.TrimSpace(message.Partial)
		}
		if text == "" && len(message.Sentences) > 0 {
			var b strings.Builder
			for _, sentence := range message.Sentences {
				b.WriteString(sentence.Text)
			}
			text = strings.TrimSpace(b.String())
		}

		final := message.IsFinal || (message.IsEnd && message.IsFinal)
		stable := final || !strings.Contains(message.Mode, "online")
		if text != "" {
			s.mu.Lock()
			if final || stable {
				s.text = text
			} else if s.text == "" {
				s.text = text
			}
			s.mu.Unlock()
			if err := s.emit(TranscriptEvent{Text: text, Final: final, Stable: stable}); err != nil {
				s.finish(err)
				return
			}
		}
		if message.IsEnd && message.IsFinal {
			s.finish(nil)
			return
		}
	}
}

func (s *funASRStream) Push(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageBinary, pcm)
}

func (s *funASRStream) CloseInput(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	payload := []byte(`{"is_speaking":false,"is_end":true}`)
	if err := s.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("finish FunASR input: %w", err)
	}
	return nil
}

func (s *funASRStream) Wait(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.text, s.err
	}
}

func (s *funASRStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	return s.conn.Close(websocket.StatusNormalClosure, "companion speech stream closed")
}

var _ StreamingASRProvider = (*FunASRProvider)(nil)
