package protocol

import "fmt"

const (
	Version                 = 1
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
)

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

type RuntimeConfig struct {
	SmartVADEnabled *bool  `json:"smart_vad_enabled,omitempty"`
	VADThreshold    *int   `json:"vad_threshold,omitempty"`
	VADSilenceMS    *int   `json:"vad_silence_ms,omitempty"`
	VADMinSpeechMS  *int   `json:"vad_min_speech_ms,omitempty"`
	IdleAfterMS     *int   `json:"idle_after_ms,omitempty"`
	AlarmVisibleMS  *int   `json:"alarm_visible_ms,omitempty"`
	Locale          string `json:"locale,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	VoiceKey        string `json:"voice_key,omitempty"`
}

type Features struct {
	StreamingTTS  bool `json:"streaming_tts,omitempty"`
	ButtonBargeIn bool `json:"button_barge_in,omitempty"`
}

type Message struct {
	Type          string         `json:"type"`
	Version       int            `json:"version,omitempty"`
	Transport     string         `json:"transport,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	TurnID        string         `json:"turn_id,omitempty"`
	State         string         `json:"state,omitempty"`
	Mode          string         `json:"mode,omitempty"`
	Text          string         `json:"text,omitempty"`
	ID            string         `json:"id,omitempty"`
	Message       string         `json:"message,omitempty"`
	FireAt        string         `json:"fire_at,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	Code          string         `json:"code,omitempty"`
	Features      Features       `json:"features,omitempty"`
	AudioParams   *AudioParams   `json:"audio_params,omitempty"`
	UI            any            `json:"ui,omitempty"`
	Config        *RuntimeConfig `json:"config,omitempty"`
	ConfigVersion int64          `json:"config_version,omitempty"`
	Applied       bool           `json:"applied,omitempty"`
}

func ValidateHello(message Message) error {
	if message.Type != "hello" {
		return fmt.Errorf("first message must be hello")
	}
	if message.Version != Version {
		return fmt.Errorf("unsupported protocol version %d", message.Version)
	}
	if message.Transport != Transport {
		return fmt.Errorf("unsupported transport %q", message.Transport)
	}
	if message.AudioParams == nil {
		return fmt.Errorf("audio_params is required")
	}
	want := DefaultAudioParams()
	got := *message.AudioParams
	if got != want {
		return fmt.Errorf("unsupported audio params: got %+v, want %+v", got, want)
	}
	return nil
}
