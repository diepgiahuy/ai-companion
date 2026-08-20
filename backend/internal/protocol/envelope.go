package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	Version                 = 2
	Transport               = "websocket"
	AudioFormat             = "opus"
	UplinkSampleRate        = 16000
	DownlinkSampleRate      = 24000
	Channels                = 1
	FrameDurationMS         = 60
	UplinkSamplesPerFrame   = UplinkSampleRate * FrameDurationMS / 1000
	DownlinkSamplesPerFrame = DownlinkSampleRate * FrameDurationMS / 1000
	MaximumOpusPacketBytes  = 1275
	MaximumAudioSecs        = 8
	MaximumEnvelopeBytes    = 8192
	MaximumPayloadBytes     = 4096
)

const (
	UnsupportedProtocolVersionCode = "unsupported_protocol_version"
	UnknownMessageTypeCode         = "unknown_message_type"
	InvalidEnvelopeCode            = "invalid_envelope"
)

type ProtocolError struct {
	Code   string
	Detail string
}

func (e *ProtocolError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func ErrorCode(err error) string {
	var protocolError *ProtocolError
	if errors.As(err, &protocolError) {
		return protocolError.Code
	}
	return InvalidEnvelopeCode
}

type MessageType string

type ProtocolVersion int

func (v *ProtocolVersion) UnmarshalJSON(data []byte) error {
	const maximumExactJSONInteger = 9_007_199_254_740_991
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] == '"' {
		return fmt.Errorf("version must be an integer-valued JSON number")
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return fmt.Errorf("version must be an integer-valued JSON number: %w", err)
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.Abs(parsed) > maximumExactJSONInteger || math.Trunc(parsed) != parsed {
		return fmt.Errorf("version must be an exact integer-valued JSON number")
	}
	*v = ProtocolVersion(int64(parsed))
	return nil
}

const (
	SessionHelloType    MessageType = "session.hello"
	SessionReadyType    MessageType = "session.ready"
	SessionPingType     MessageType = "session.ping"
	SessionPongType     MessageType = "session.pong"
	TurnListenType      MessageType = "turn.listen"
	TurnAbortType       MessageType = "turn.abort"
	TurnStateType       MessageType = "turn.state"
	TranscriptFinalType MessageType = "transcript.final"
	TTSLifecycleType    MessageType = "tts.lifecycle"
	AgentStatusType     MessageType = "agent.status"
	UICardType          MessageType = "ui.card"
	UIStateType         MessageType = "ui.state"
	AlarmFiredType      MessageType = "alarm.fired"
	AlarmAckType        MessageType = "alarm.ack"
	ScheduleUpdatedType MessageType = "schedule.updated"
	ProtocolErrorType   MessageType = "protocol.error"
)

func (t MessageType) Valid() bool {
	switch t {
	case SessionHelloType, SessionReadyType, SessionPingType, SessionPongType,
		TurnListenType, TurnAbortType, TurnStateType, TranscriptFinalType,
		TTSLifecycleType, AgentStatusType, UICardType, UIStateType,
		AlarmFiredType, AlarmAckType, ScheduleUpdatedType, ProtocolErrorType,
		CapabilityAdvertiseType, CapabilityCallType, CapabilityResultType, CapabilityCancelType,
		GestureNotificationType,
		VoiceMailAvailableType, VoiceMailClaimType, VoiceMailClaimedType,
		VoiceMailPlaybackResultType, VoiceMailConsumedType, VoiceMailExpiredType,
		PairingSessionCreateType, PairingSessionCreatedType, PairingConfirmationType,
		PairingSucceededType, PairingRejectedType, PairingExpiredType:
		return true
	default:
		return false
	}
}

type Envelope struct {
	Version        ProtocolVersion `json:"version"`
	Type           MessageType     `json:"type"`
	MessageID      string          `json:"message_id"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	GenerationID   uint64          `json:"generation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	OccurredAt     string          `json:"occurred_at,omitempty"`
	Payload        json.RawMessage `json:"payload"`
}

type Metadata struct {
	MessageID      string
	CorrelationID  string
	SessionID      string
	TurnID         string
	GenerationID   uint64
	IdempotencyKey string
	OccurredAt     string
}

func (e Envelope) Validate() error {
	if e.Version != Version {
		return &ProtocolError{
			Code:   UnsupportedProtocolVersionCode,
			Detail: fmt.Sprintf("got %d, want %d", e.Version, Version),
		}
	}
	if !e.Type.Valid() {
		return &ProtocolError{Code: UnknownMessageTypeCode, Detail: fmt.Sprintf("%q", e.Type)}
	}
	if err := validateOpaqueID("message_id", e.MessageID, 128); err != nil {
		return &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
	}
	if e.CorrelationID != "" {
		if err := validateOpaqueID("correlation_id", e.CorrelationID, 128); err != nil {
			return &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
		}
	}
	if e.SessionID != "" {
		if err := validateOpaqueID("session_id", e.SessionID, 128); err != nil {
			return &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
		}
	}
	if e.TurnID != "" {
		if err := validateOpaqueID("turn_id", e.TurnID, 128); err != nil {
			return &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
		}
	}
	if len(e.Payload) == 0 || len(e.Payload) > MaximumPayloadBytes {
		return &ProtocolError{Code: InvalidEnvelopeCode, Detail: "payload must be a non-empty object within the size limit"}
	}
	trimmed := bytes.TrimSpace(e.Payload)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || !json.Valid(trimmed) {
		return &ProtocolError{Code: InvalidEnvelopeCode, Detail: "payload must be a JSON object"}
	}
	return nil
}

func Encode(messageType MessageType, metadata Metadata, payload any) ([]byte, error) {
	if payload == nil {
		return nil, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "payload is required"}
	}
	if validator, ok := payload.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return nil, &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "encode payload: " + err.Error()}
	}
	envelope := Envelope{
		Version:        Version,
		Type:           messageType,
		MessageID:      metadata.MessageID,
		CorrelationID:  metadata.CorrelationID,
		SessionID:      metadata.SessionID,
		TurnID:         metadata.TurnID,
		GenerationID:   metadata.GenerationID,
		IdempotencyKey: metadata.IdempotencyKey,
		OccurredAt:     metadata.OccurredAt,
		Payload:        raw,
	}
	if err := envelope.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "encode envelope: " + err.Error()}
	}
	if len(encoded) > MaximumEnvelopeBytes {
		return nil, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "envelope exceeds size limit"}
	}
	return encoded, nil
}

func Decode(data []byte) (Envelope, error) {
	if len(data) == 0 || len(data) > MaximumEnvelopeBytes {
		return Envelope{}, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "envelope is empty or exceeds size limit"}
	}

	// Read the version before strict schema decoding so every v1 client gets the
	// stable breaking-change error, even though its flat fields are unknown in v2.
	var header struct {
		Version *ProtocolVersion `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Envelope{}, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "decode envelope: " + err.Error()}
	}
	if header.Version == nil {
		return Envelope{}, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "version is required"}
	}
	if *header.Version != Version {
		return Envelope{}, &ProtocolError{
			Code:   UnsupportedProtocolVersionCode,
			Detail: fmt.Sprintf("got %d, want %d", *header.Version, Version),
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "decode envelope: " + err.Error()}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Envelope{}, &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}

func DecodePayload[T any](envelope Envelope) (T, error) {
	var payload T
	decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, &ProtocolError{Code: InvalidEnvelopeCode, Detail: "decode payload: " + err.Error()}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return payload, &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
	}
	if validator, ok := any(payload).(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return payload, &ProtocolError{Code: InvalidEnvelopeCode, Detail: err.Error()}
		}
	}
	return payload, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected data after JSON object")
		}
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return nil
}

type EmptyPayload struct{}

type AudioParams struct {
	Format          string `json:"format"`
	SampleRate      int    `json:"sample_rate"`
	Channels        int    `json:"channels"`
	FrameDurationMS int    `json:"frame_duration"`
}

func DefaultAudioParams() AudioParams {
	return AudioParams{
		Format:          AudioFormat,
		SampleRate:      UplinkSampleRate,
		Channels:        Channels,
		FrameDurationMS: FrameDurationMS,
	}
}

func DownlinkAudioParams() AudioParams {
	return AudioParams{
		Format: AudioFormat, SampleRate: DownlinkSampleRate,
		Channels: Channels, FrameDurationMS: FrameDurationMS,
	}
}

type Features struct {
	StreamingTTS  bool `json:"streaming_tts,omitempty"`
	ButtonBargeIn bool `json:"button_barge_in,omitempty"`
}

type HelloPayload struct {
	Transport   string      `json:"transport"`
	AudioParams AudioParams `json:"audio_params"`
	Features    *Features   `json:"features,omitempty"`
}

type ReadyPayload struct {
	Transport   string      `json:"transport"`
	AudioParams AudioParams `json:"audio_params"`
	Features    *Features   `json:"features,omitempty"`
}

func (p ReadyPayload) Validate() error {
	if p.Transport != Transport {
		return fmt.Errorf("unsupported transport %q", p.Transport)
	}
	if p.AudioParams != DownlinkAudioParams() {
		return fmt.Errorf("unsupported audio params: got %+v, want %+v", p.AudioParams, DownlinkAudioParams())
	}
	return nil
}

func (p HelloPayload) Validate() error {
	if p.Transport != Transport {
		return fmt.Errorf("unsupported transport %q", p.Transport)
	}
	if p.AudioParams != DefaultAudioParams() {
		return fmt.Errorf("unsupported audio params: got %+v, want %+v", p.AudioParams, DefaultAudioParams())
	}
	return nil
}

func ValidateHello(envelope Envelope, payload HelloPayload) error {
	if envelope.Type != SessionHelloType {
		return fmt.Errorf("first message must be %s", SessionHelloType)
	}
	if envelope.SessionID != "" || envelope.TurnID != "" {
		return fmt.Errorf("hello must not claim a session or turn")
	}
	return payload.Validate()
}

type ListenPayload struct {
	State string `json:"state"`
	Mode  string `json:"mode,omitempty"`
}

func (p ListenPayload) Validate() error {
	if p.State != "start" && p.State != "stop" {
		return fmt.Errorf("listen state must be start or stop")
	}
	if p.State == "start" && p.Mode != "manual" && p.Mode != "auto_vad" {
		return fmt.Errorf("listen start mode must be manual or auto_vad")
	}
	if p.State == "stop" && p.Mode != "" {
		return fmt.Errorf("listen stop must not include mode")
	}
	return nil
}

type AbortPayload struct {
	Reason string `json:"reason"`
}

func (p AbortPayload) Validate() error {
	if strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 64 {
		return fmt.Errorf("abort reason must be 1..64 bytes")
	}
	return nil
}

type AlarmAckPayload struct {
	AlarmID string `json:"alarm_id"`
}

func (p AlarmAckPayload) Validate() error { return validateOpaqueID("alarm_id", p.AlarmID, 128) }

type AlarmFiredPayload struct {
	AlarmID string `json:"alarm_id"`
	Message string `json:"message"`
	FireAt  string `json:"fire_at"`
}

func (p AlarmFiredPayload) Validate() error {
	if err := validateOpaqueID("alarm_id", p.AlarmID, 128); err != nil {
		return err
	}
	if strings.TrimSpace(p.Message) == "" || len(p.Message) > 512 {
		return fmt.Errorf("alarm message must be 1..512 bytes")
	}
	if strings.TrimSpace(p.FireAt) == "" {
		return fmt.Errorf("fire_at is required")
	}
	return nil
}

type ScheduleUpdatedPayload struct {
	Message string `json:"message"`
	FireAt  string `json:"fire_at"`
}

func (p ScheduleUpdatedPayload) Validate() error {
	if strings.TrimSpace(p.Message) == "" || len(p.Message) > 512 {
		return fmt.Errorf("schedule message must be 1..512 bytes")
	}
	if strings.TrimSpace(p.FireAt) == "" {
		return fmt.Errorf("fire_at is required")
	}
	return nil
}

type TextPayload struct {
	Text string `json:"text"`
}

func (p TextPayload) Validate() error {
	if strings.TrimSpace(p.Text) == "" || len(p.Text) > MaximumPayloadBytes {
		return fmt.Errorf("text must be non-empty and within the payload limit")
	}
	return nil
}

type TTSLifecyclePayload struct {
	State string `json:"state"`
	Text  string `json:"text,omitempty"`
}

func (p TTSLifecyclePayload) Validate() error {
	switch p.State {
	case "start", "stop":
		if p.Text != "" {
			return fmt.Errorf("text is only valid for sentence lifecycle states")
		}
	case "sentence_start", "sentence_end":
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("sentence lifecycle state requires text")
		}
	default:
		return fmt.Errorf("unsupported TTS lifecycle state %q", p.State)
	}
	return nil
}

type TurnStatePayload struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

func (p TurnStatePayload) Validate() error {
	switch p.State {
	case "listening", "processing", "speaking", "completed":
		if p.Reason != "" {
			return fmt.Errorf("reason is only valid for interrupted state")
		}
	case "interrupted":
		if strings.TrimSpace(p.Reason) == "" || len(p.Reason) > 64 {
			return fmt.Errorf("interrupted state requires a bounded reason")
		}
	default:
		return fmt.Errorf("unsupported turn state %q", p.State)
	}
	return nil
}

type AgentStatusPayload struct {
	State string `json:"state"`
}

func (p AgentStatusPayload) Validate() error {
	if strings.TrimSpace(p.State) == "" || len(p.State) > 64 {
		return fmt.Errorf("agent state must be 1..64 bytes")
	}
	return nil
}

type UICardPayload struct {
	UI any `json:"ui"`
}

func (p UICardPayload) Validate() error {
	if p.UI == nil {
		return fmt.Errorf("ui is required")
	}
	raw, err := json.Marshal(p.UI)
	if err != nil {
		return fmt.Errorf("encode ui: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return fmt.Errorf("ui must be a JSON object")
	}
	return nil
}

type UIEmotion string

const (
	UIEmotionIdle          UIEmotion = "idle"
	UIEmotionListening     UIEmotion = "listening"
	UIEmotionThinking      UIEmotion = "thinking"
	UIEmotionSpeaking      UIEmotion = "speaking"
	UIEmotionToolExecuting UIEmotion = "tool_executing"
	UIEmotionInterrupted   UIEmotion = "interrupted"
	UIEmotionError         UIEmotion = "error"
)

func (e UIEmotion) Valid() bool {
	switch e {
	case UIEmotionIdle, UIEmotionListening, UIEmotionThinking, UIEmotionSpeaking,
		UIEmotionToolExecuting, UIEmotionInterrupted, UIEmotionError:
		return true
	default:
		return false
	}
}

type UIStatePayload struct {
	Emotion  UIEmotion `json:"emotion"`
	ToolName string    `json:"tool_name,omitempty"`
}

func (p UIStatePayload) Validate() error {
	if !p.Emotion.Valid() {
		return fmt.Errorf("unsupported ui emotion %q", p.Emotion)
	}
	if p.Emotion == UIEmotionToolExecuting && p.ToolName == "" {
		return fmt.Errorf("tool_name is required for tool_executing emotion")
	}
	if p.Emotion != UIEmotionToolExecuting && p.ToolName != "" {
		return fmt.Errorf("tool_name is only valid for tool_executing emotion")
	}
	return nil
}

func ValidateUIState(payload UIStatePayload) error { return payload.Validate() }

type ProtocolErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (p ProtocolErrorPayload) Validate() error {
	if strings.TrimSpace(p.Code) == "" || len(p.Code) > 64 {
		return fmt.Errorf("error code must be 1..64 bytes")
	}
	if strings.TrimSpace(p.Message) == "" || len(p.Message) > 1024 {
		return fmt.Errorf("error message must be 1..1024 bytes")
	}
	return nil
}

func validateOpaqueID(name, value string, max int) error {
	if strings.TrimSpace(value) == "" || len(value) > max {
		return fmt.Errorf("%s must be 1..%d bytes", name, max)
	}
	return nil
}
