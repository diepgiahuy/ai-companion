package speech

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode"

	"github.com/coder/websocket"
)

const (
	defaultGeminiLiveURL = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
	// Gemini Live carries base64-encoded PCM inside JSON and real provider frames
	// can exceed coder/websocket's 32 KiB default. Keep a bounded provider-specific
	// ceiling rather than disabling read limits entirely.
	geminiLiveReadLimit = 256 * 1024
)

type GeminiLiveConfig struct {
	URL          string
	Model        string
	APIKey       string
	Voice        string
	Instructions string
	Tools        []NativeRealtimeTool
	HTTPClient   *http.Client
}

func (c GeminiLiveConfig) normalized() (GeminiLiveConfig, error) {
	c.URL = strings.TrimSpace(c.URL)
	c.Model = strings.TrimSpace(c.Model)
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.Voice = strings.TrimSpace(c.Voice)
	c.Instructions = strings.TrimSpace(c.Instructions)
	if c.URL == "" {
		c.URL = defaultGeminiLiveURL
	}
	if c.Model == "" {
		c.Model = "gemini-3.1-flash-live-preview"
	}
	if c.APIKey == "" {
		return c, errors.New("Gemini Live API key is required")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return c, fmt.Errorf("invalid Gemini Live URL %q", c.URL)
	}
	if parsed.Scheme != "wss" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return c, errors.New("Gemini Live URL must use wss outside localhost")
	}
	return c, nil
}

type GeminiLiveProvider struct {
	config GeminiLiveConfig
}

func NewGeminiLive(config GeminiLiveConfig) (*GeminiLiveProvider, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &GeminiLiveProvider{config: normalized}, nil
}

func (p *GeminiLiveProvider) Connect(ctx context.Context) (NativeRealtimeSession, error) {
	if p == nil {
		return nil, errors.New("Gemini Live provider is nil")
	}
	parsed, err := url.Parse(p.config.URL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("key", p.config.APIKey)
	parsed.RawQuery = query.Encode()
	headers := http.Header{}
	headers.Set("User-Agent", "ai-companion/gemini-live-reference")
	options := &websocket.DialOptions{HTTPHeader: headers}
	if p.config.HTTPClient != nil {
		options.HTTPClient = p.config.HTTPClient
	}
	conn, _, err := websocket.Dial(ctx, parsed.String(), options)
	if err != nil {
		message := strings.ReplaceAll(err.Error(), p.config.APIKey, "***")
		return nil, fmt.Errorf("dial Gemini Live: %s", message)
	}
	conn.SetReadLimit(geminiLiveReadLimit)
	aliases, declarations, err := geminiLiveTools(p.config.Tools)
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	s := &geminiLiveSession{
		conn:             conn,
		aliasToCanonical: aliases,
		callNames:        map[string]string{},
	}
	setup := map[string]any{
		"model": "models/" + p.config.Model,
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
		},
		"realtimeInputConfig": map[string]any{
			"automaticActivityDetection": map[string]any{"disabled": true},
			"activityHandling":            "START_OF_ACTIVITY_INTERRUPTS",
			"turnCoverage":                "TURN_INCLUDES_ONLY_ACTIVITY",
		},
		"inputAudioTranscription":  map[string]any{},
		"outputAudioTranscription": map[string]any{},
		"sessionResumption":         map[string]any{},
	}
	if p.config.Voice != "" {
		setup["generationConfig"].(map[string]any)["speechConfig"] = map[string]any{
			"voiceConfig": map[string]any{
				"prebuiltVoiceConfig": map[string]any{"voiceName": p.config.Voice},
			},
		}
	}
	if p.config.Instructions != "" {
		setup["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": p.config.Instructions}},
		}
	}
	if len(declarations) > 0 {
		setup["tools"] = []map[string]any{{"functionDeclarations": declarations}}
	}
	if err := s.writeJSON(ctx, map[string]any{"setup": setup}); err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("configure Gemini Live session: %w", err)
	}
	for {
		wire, err := s.readWire(ctx)
		if err != nil {
			conn.CloseNow()
			return nil, fmt.Errorf("wait for Gemini Live setup: %w", err)
		}
		if wire.SetupComplete != nil {
			return s, nil
		}
		if wire.Error != nil {
			conn.CloseNow()
			return nil, fmt.Errorf("Gemini Live setup error %s: %s", wire.Error.Code, wire.Error.Message)
		}
	}
}

type geminiLiveSession struct {
	conn             *websocket.Conn
	writeMu          sync.Mutex
	stateMu          sync.Mutex
	closeOnce        sync.Once
	activityOpen     bool
	aliasToCanonical map[string]string
	callNames        map[string]string
	pending          []NativeRealtimeEvent
}

func (s *geminiLiveSession) AppendAudio(ctx context.Context, pcm []byte) error {
	if len(pcm) == 0 {
		return nil
	}
	if len(pcm)%2 != 0 {
		return errors.New("Gemini Live PCM16 input must contain an even number of bytes")
	}
	if err := s.ensureActivityStarted(ctx); err != nil {
		return err
	}
	return s.writeJSON(ctx, map[string]any{
		"realtimeInput": map[string]any{
			"audio": map[string]any{
				"data":     base64.StdEncoding.EncodeToString(pcm),
				"mimeType": "audio/pcm;rate=16000",
			},
		},
	})
}

func (s *geminiLiveSession) CommitAudio(ctx context.Context) error {
	if err := s.ensureActivityStarted(ctx); err != nil {
		return err
	}
	if err := s.writeJSON(ctx, map[string]any{"realtimeInput": map[string]any{"activityEnd": map[string]any{}}}); err != nil {
		return err
	}
	s.stateMu.Lock()
	s.activityOpen = false
	s.stateMu.Unlock()
	return nil
}

// Gemini Live starts model generation from the manual activityEnd boundary, so
// there is no separate response.create message to send.
func (s *geminiLiveSession) CreateResponse(context.Context) error { return nil }

func (s *geminiLiveSession) CancelResponse(ctx context.Context) error {
	// With manual VAD and START_OF_ACTIVITY_INTERRUPTS, activityStart is the
	// provider-native barge-in signal. The cancellation benchmark closes the
	// session after observing interrupted/turnComplete, so no synthetic empty
	// activityEnd is sent here.
	return s.writeJSON(ctx, map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}})
}

func (s *geminiLiveSession) ReturnToolResult(ctx context.Context, callID, output string) error {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("Gemini Live tool call id is required")
	}
	s.stateMu.Lock()
	name := s.callNames[callID]
	s.stateMu.Unlock()
	if name == "" {
		return fmt.Errorf("Gemini Live tool call %q is unknown", callID)
	}
	var result any = output
	var decoded any
	if strings.TrimSpace(output) != "" && json.Unmarshal([]byte(output), &decoded) == nil {
		result = decoded
	}
	return s.writeJSON(ctx, map[string]any{
		"toolResponse": map[string]any{
			"functionResponses": []map[string]any{{
				"name": name,
				"id":   callID,
				"response": map[string]any{
					"result": result,
				},
			}},
		},
	})
}

func (s *geminiLiveSession) ensureActivityStarted(ctx context.Context) error {
	s.stateMu.Lock()
	if s.activityOpen {
		s.stateMu.Unlock()
		return nil
	}
	s.activityOpen = true
	s.stateMu.Unlock()
	if err := s.writeJSON(ctx, map[string]any{"realtimeInput": map[string]any{"activityStart": map[string]any{}}}); err != nil {
		s.stateMu.Lock()
		s.activityOpen = false
		s.stateMu.Unlock()
		return err
	}
	return nil
}

func (s *geminiLiveSession) writeJSON(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, raw)
}

func (s *geminiLiveSession) readWire(ctx context.Context) (geminiLiveWireMessage, error) {
	kind, raw, err := s.conn.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return geminiLiveWireMessage{}, ctx.Err()
		}
		return geminiLiveWireMessage{}, fmt.Errorf("read Gemini Live event: %w", err)
	}
	if kind != websocket.MessageText && kind != websocket.MessageBinary {
		return geminiLiveWireMessage{}, fmt.Errorf("Gemini Live returned unsupported WebSocket message type %d", kind)
	}
	var wire geminiLiveWireMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return geminiLiveWireMessage{}, fmt.Errorf("decode Gemini Live JSON event: %w", err)
	}
	return wire, nil
}

func (s *geminiLiveSession) Receive(ctx context.Context) (NativeRealtimeEvent, error) {
	s.stateMu.Lock()
	if len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		s.stateMu.Unlock()
		return event, nil
	}
	s.stateMu.Unlock()

	for {
		wire, err := s.readWire(ctx)
		if err != nil {
			return NativeRealtimeEvent{}, err
		}
		if wire.Error != nil {
			return NativeRealtimeEvent{}, fmt.Errorf("Gemini Live error %s: %s", wire.Error.Code, wire.Error.Message)
		}
		if wire.ToolCall != nil && len(wire.ToolCall.FunctionCalls) > 0 {
			events := make([]NativeRealtimeEvent, 0, len(wire.ToolCall.FunctionCalls))
			for _, call := range wire.ToolCall.FunctionCalls {
				canonical, ok := s.aliasToCanonical[call.Name]
				if !ok {
					return NativeRealtimeEvent{}, fmt.Errorf("Gemini Live returned undeclared tool alias %q", call.Name)
				}
				s.stateMu.Lock()
				s.callNames[call.ID] = call.Name
				s.stateMu.Unlock()
				events = append(events, NativeRealtimeEvent{
					Type: "response.function_call_arguments.done",
					ToolCall: &NativeRealtimeToolCall{
						CallID:    call.ID,
						Name:      canonical,
						Arguments: call.Args,
					},
				})
			}
			s.stateMu.Lock()
			if len(events) > 1 {
				s.pending = append(s.pending, events[1:]...)
			}
			s.stateMu.Unlock()
			return events[0], nil
		}
		if wire.ToolCallCancellation != nil {
			return NativeRealtimeEvent{Type: "tool_call.cancelled", ResponseStatus: strings.Join(wire.ToolCallCancellation.IDs, ",")}, nil
		}
		if wire.SessionResumptionUpdate != nil {
			return NativeRealtimeEvent{
				Type:             "session.resumption.update",
				ResumptionHandle: wire.SessionResumptionUpdate.NewHandle,
				Resumable:        wire.SessionResumptionUpdate.Resumable,
			}, nil
		}
		if wire.GoAway != nil {
			return NativeRealtimeEvent{Type: "session.go_away"}, nil
		}
		if wire.ServerContent == nil {
			continue
		}
		content := wire.ServerContent
		event := NativeRealtimeEvent{Type: "server.content"}
		if content.InterimInputTranscription != nil {
			event.Type = "input_audio_transcription.delta"
			event.InputTranscript = content.InterimInputTranscription.Text
		}
		if content.InputTranscription != nil {
			event.Type = "input_audio_transcription.completed"
			event.InputTranscript = content.InputTranscription.Text
			event.InputFinal = true
		}
		if content.OutputTranscription != nil {
			event.Type = "response.audio_transcript.delta"
			event.AudioTranscript = content.OutputTranscription.Text
		}
		if content.ModelTurn != nil {
			for _, part := range content.ModelTurn.Parts {
				if part.Text != "" {
					event.TextDelta += part.Text
				}
				if part.InlineData != nil && part.InlineData.Data != "" {
					pcm, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
					if err != nil {
						return NativeRealtimeEvent{}, fmt.Errorf("decode Gemini Live PCM: %w", err)
					}
					event.AudioPCM = append(event.AudioPCM, pcm...)
				}
			}
			if len(event.AudioPCM) > 0 {
				event.Type = "response.audio.delta"
			} else if event.TextDelta != "" {
				event.Type = "response.text.delta"
			}
		}
		if content.GenerationComplete {
			event.Type = "response.audio.done"
		}
		if content.Interrupted {
			event.Type = "response.done"
			event.ResponseDone = true
			event.ResponseStatus = "cancelled"
		}
		if content.TurnComplete {
			event.Type = "response.done"
			event.ResponseDone = true
			if event.ResponseStatus == "" {
				event.ResponseStatus = "completed"
			}
		}
		return event, nil
	}
}

func (s *geminiLiveSession) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.conn.Close(websocket.StatusNormalClosure, "Gemini Live reference closed")
	})
	return err
}

type geminiLiveWireMessage struct {
	SetupComplete *struct{} `json:"setupComplete"`
	ServerContent *struct {
		GenerationComplete        bool                     `json:"generationComplete"`
		TurnComplete              bool                     `json:"turnComplete"`
		Interrupted               bool                     `json:"interrupted"`
		InputTranscription        *geminiLiveTranscription `json:"inputTranscription"`
		InterimInputTranscription *geminiLiveTranscription `json:"interimInputTranscription"`
		OutputTranscription       *geminiLiveTranscription `json:"outputTranscription"`
		ModelTurn                 *struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					Data     string `json:"data"`
					MimeType string `json:"mimeType"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"modelTurn"`
	} `json:"serverContent"`
	ToolCall *struct {
		FunctionCalls []struct {
			Name string         `json:"name"`
			ID   string         `json:"id"`
			Args map[string]any `json:"args"`
		} `json:"functionCalls"`
	} `json:"toolCall"`
	ToolCallCancellation *struct {
		IDs []string `json:"ids"`
	} `json:"toolCallCancellation"`
	SessionResumptionUpdate *struct {
		NewHandle string `json:"newHandle"`
		Resumable bool   `json:"resumable"`
	} `json:"sessionResumptionUpdate"`
	GoAway *struct {
		TimeLeft string `json:"timeLeft"`
	} `json:"goAway"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type geminiLiveTranscription struct {
	Text string `json:"text"`
}

func geminiLiveTools(tools []NativeRealtimeTool) (map[string]string, []map[string]any, error) {
	aliases := make(map[string]string, len(tools))
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		canonical := strings.TrimSpace(tool.Name)
		if canonical == "" {
			return nil, nil, errors.New("Gemini Live tool name is required")
		}
		alias := geminiLiveToolAlias(canonical)
		if previous, exists := aliases[alias]; exists && previous != canonical {
			return nil, nil, fmt.Errorf("Gemini Live tool alias collision for %q and %q", previous, canonical)
		}
		aliases[alias] = canonical
		declaration := map[string]any{"name": alias, "description": tool.Description}
		if tool.Parameters != nil {
			declaration["parameters"] = geminiLiveSchema(tool.Parameters)
		}
		result = append(result, declaration)
	}
	return aliases, result, nil
}

// Gemini's FunctionDeclaration parameters use the Gemini Schema subset rather
// than arbitrary JSON Schema. Companion definitions commonly include
// additionalProperties for server-side validation; Gemini rejects that keyword
// during Live setup, so the reference adapter strips it recursively while
// leaving Companion's authoritative ToolRegistry schema unchanged.
func geminiLiveSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "additionalProperties" {
				continue
			}
			out[key] = geminiLiveSchema(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = geminiLiveSchema(child)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func geminiLiveToolAlias(canonical string) string {
	var slug strings.Builder
	for _, r := range canonical {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			slug.WriteRune(r)
		} else {
			slug.WriteByte('_')
		}
		if slug.Len() >= 40 {
			break
		}
	}
	if slug.Len() == 0 || unicode.IsDigit([]rune(slug.String())[0]) {
		slug.WriteString("tool")
	}
	sum := sha256.Sum256([]byte(canonical))
	return "c_" + slug.String() + "_" + hex.EncodeToString(sum[:6])
}

var _ NativeRealtimeProvider = (*GeminiLiveProvider)(nil)
var _ NativeRealtimeSession = (*geminiLiveSession)(nil)
