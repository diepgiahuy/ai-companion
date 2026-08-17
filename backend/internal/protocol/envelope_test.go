package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelloMatchesCrossRuntimeGoldenFixture(t *testing.T) {
	wire, err := Encode(SessionHelloType, Metadata{MessageID: "firmware-1"}, HelloPayload{
		Transport: Transport, AudioParams: DefaultAudioParams(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "protocol", "v2", "session_hello.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(wire), strings.TrimSpace(string(fixture)); got != want {
		t.Fatalf("Go encoder drifted from cross-runtime fixture\ngot:  %s\nwant: %s", got, want)
	}
}

func TestHelloEnvelopeRoundTrip(t *testing.T) {
	wire, err := Encode(SessionHelloType, Metadata{MessageID: "device-1"}, HelloPayload{
		Transport: Transport, AudioParams: DefaultAudioParams(),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodePayload[HelloPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateHello(envelope, payload); err != nil {
		t.Fatalf("valid hello rejected: %v", err)
	}
	if envelope.Version != 2 || envelope.Type != SessionHelloType || envelope.MessageID != "device-1" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func TestVersionOneFailsWithStableCodeBeforeFlatSchemaValidation(t *testing.T) {
	legacy := []byte(`{"type":"hello","version":1,"transport":"websocket","audio_params":{}}`)
	_, err := Decode(legacy)
	if err == nil {
		t.Fatal("legacy v1 message was accepted")
	}
	if got := ErrorCode(err); got != UnsupportedProtocolVersionCode {
		t.Fatalf("error code = %q, want %q: %v", got, UnsupportedProtocolVersionCode, err)
	}
}

func TestVersionSyntaxUsesStableErrorTaxonomy(t *testing.T) {
	tests := []struct {
		name string
		wire string
		code string
	}{
		{name: "integer valued decimal", wire: `{"version":2.0,"type":"session.ping","message_id":"m-1","payload":{}}`},
		{name: "integer valued legacy", wire: `{"version":1.0}`, code: UnsupportedProtocolVersionCode},
		{name: "missing", wire: `{"type":"session.ping","message_id":"m-1","payload":{}}`, code: InvalidEnvelopeCode},
		{name: "null", wire: `{"version":null}`, code: InvalidEnvelopeCode},
		{name: "fractional", wire: `{"version":2.5}`, code: InvalidEnvelopeCode},
		{name: "string", wire: `{"version":"2"}`, code: InvalidEnvelopeCode},
		{name: "outside exact range", wire: `{"version":9007199254740992}`, code: InvalidEnvelopeCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.wire))
			if test.code == "" {
				if err != nil {
					t.Fatalf("valid version rejected: %v", err)
				}
				return
			}
			if got := ErrorCode(err); got != test.code {
				t.Fatalf("error code = %q, want %q: %v", got, test.code, err)
			}
		})
	}
}

func TestEnvelopeRejectsUnknownTypeAndFlatV2Fields(t *testing.T) {
	unknown := []byte(`{"version":2,"type":"future.message","message_id":"m-1","payload":{}}`)
	if _, err := Decode(unknown); ErrorCode(err) != UnknownMessageTypeCode {
		t.Fatalf("unknown type error = %v", err)
	}

	flat := []byte(`{"version":2,"type":"session.hello","message_id":"m-1","transport":"websocket","payload":{}}`)
	if _, err := Decode(flat); ErrorCode(err) != InvalidEnvelopeCode {
		t.Fatalf("flat v2 field error = %v", err)
	}
}

func TestEnvelopeRequiresObjectPayload(t *testing.T) {
	for name, payload := range map[string]json.RawMessage{
		"missing": nil,
		"array":   json.RawMessage(`[]`),
		"scalar":  json.RawMessage(`"value"`),
	} {
		t.Run(name, func(t *testing.T) {
			envelope := Envelope{Version: Version, Type: SessionPingType, MessageID: "m-1", Payload: payload}
			if err := envelope.Validate(); err == nil {
				t.Fatal("invalid payload was accepted")
			}
		})
	}
}

func TestHelloRejectsUnsupportedAudio(t *testing.T) {
	payload := HelloPayload{
		Transport:   Transport,
		AudioParams: AudioParams{Format: "pcm_s16le", SampleRate: 16000, Channels: 1, FrameDurationMS: 20},
	}
	_, err := Encode(SessionHelloType, Metadata{MessageID: "device-1"}, payload)
	if err == nil || !strings.Contains(err.Error(), "unsupported audio params") {
		t.Fatalf("unsupported audio error = %v", err)
	}
}

func TestReadyIsHandshakeOnlyAndRequiresExactDownlinkAudio(t *testing.T) {
	ready := ReadyPayload{Transport: Transport, AudioParams: DownlinkAudioParams()}
	wire, err := Encode(SessionReadyType, Metadata{MessageID: "server-1", SessionID: "session-1"}, ready)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "config") {
		t.Fatalf("session.ready must not carry settings/config state: %s", wire)
	}
	ready.AudioParams.SampleRate = UplinkSampleRate
	if err := ready.Validate(); err == nil {
		t.Fatal("16 kHz session.ready was accepted")
	}
	ready.AudioParams = DownlinkAudioParams()
	ready.AudioParams.FrameDurationMS = 20
	if err := ready.Validate(); err == nil {
		t.Fatal("20 ms session.ready was accepted")
	}
	fractional := Envelope{Payload: json.RawMessage(
		`{"transport":"websocket","audio_params":{"format":"opus","sample_rate":24000,"channels":1,"frame_duration":60.5}}`,
	)}
	if _, err := DecodePayload[ReadyPayload](fractional); err == nil {
		t.Fatal("fractional session.ready audio parameter was accepted")
	}
}

func TestValidateUIState(t *testing.T) {
	valid := UIStatePayload{Emotion: UIEmotionToolExecuting, ToolName: "expense.log"}
	if err := ValidateUIState(valid); err != nil {
		t.Fatalf("valid ui state rejected: %v", err)
	}
	if err := ValidateUIState(UIStatePayload{Emotion: UIEmotionToolExecuting}); err == nil {
		t.Fatal("tool executing state without tool_name was accepted")
	}
	if err := ValidateUIState(UIStatePayload{Emotion: "dancing"}); err == nil {
		t.Fatal("unknown emotion was accepted")
	}
	if err := ValidateUIState(UIStatePayload{Emotion: UIEmotionSpeaking, ToolName: "expense.log"}); err == nil {
		t.Fatal("tool_name leaked into a non-tool state")
	}
}
