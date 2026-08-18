package protocol

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func goldenPayloads() []any {
	at := time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)
	expires := at.Add(time.Hour)
	initiator := PairingParticipant{OwnerUserID: "owner-a", DeviceID: "device-a"}
	peer := PairingParticipant{OwnerUserID: "owner-b", DeviceID: "device-b"}
	return []any{
		HelloPayload{Transport: Transport, AudioParams: DefaultAudioParams()},
		ReadyPayload{Transport: Transport, AudioParams: DownlinkAudioParams()},
		EmptyPayload{},
		EmptyPayload{},
		ListenPayload{State: "start", Mode: "manual"},
		AbortPayload{Reason: "test"},
		TurnStatePayload{State: "listening"},
		TextPayload{Text: "hello"},
		TTSLifecyclePayload{State: "start"},
		AgentStatusPayload{State: "ready"},
		UICardPayload{UI: map[string]any{"kind": "text"}},
		UIStatePayload{Emotion: UIEmotionIdle},
		AlarmFiredPayload{AlarmID: "alarm-1", Message: "wake", FireAt: at.Format(time.RFC3339)},
		AlarmAckPayload{AlarmID: "alarm-1"},
		ScheduleUpdatedPayload{Message: "wake", FireAt: at.Format(time.RFC3339)},
		ProtocolErrorPayload{Code: "test_error", Message: "test"},
		GestureNotification{Gesture: "pat", SenderDeviceID: "device-a"},
		VoiceMailAvailable{VoiceMailID: "voice-1", FromDeviceID: "device-a", MediaFormat: "ogg_opus", DurationMS: 1000, SizeBytes: 2000, ChecksumSHA256: strings.Repeat("a", 64), ExpiresAt: expires, Policy: VoiceMailPolicyEphemeral},
		VoiceMailClaim{VoiceMailID: "voice-1", PlaybackID: "play-1"},
		VoiceMailClaimed{VoiceMailID: "voice-1", PlaybackID: "play-1", MediaRef: "media-ref-1", LeaseExpiresAt: expires},
		VoiceMailPlaybackResult{VoiceMailID: "voice-1", PlaybackID: "play-1", Result: PlaybackSucceeded},
		VoiceMailConsumed{VoiceMailID: "voice-1", PlaybackID: "play-1"},
		VoiceMailExpired{VoiceMailID: "voice-1"},
		PairingSessionCreate{Initiator: initiator, CandidateDeviceID: "device-b", ProximityEvidenceID: "observation-1"},
		PairingSessionCreated{SessionID: "session-1", Initiator: initiator, Peer: peer, ExpiresAt: expires},
		PairingConfirmation{SessionID: "session-1", Participant: initiator, ConfirmationNonce: "0123456789abcdef", ConfirmedAt: at},
		PairingSucceeded{SessionID: "session-1", RelationshipID: "relationship-1", Initiator: initiator, Peer: peer},
		PairingRejected{SessionID: "session-1", Reason: "user_declined"},
		PairingExpired{SessionID: "session-1"},
	}
}

func TestGoldenEnvelopeVectorsCoverEveryMessageTypeAndTypedPayload(t *testing.T) {
	types := []MessageType{
		SessionHelloType, SessionReadyType, SessionPingType, SessionPongType,
		TurnListenType, TurnAbortType, TurnStateType, TranscriptFinalType,
		TTSLifecycleType, AgentStatusType, UICardType, UIStateType,
		AlarmFiredType, AlarmAckType, ScheduleUpdatedType, ProtocolErrorType,
		GestureNotificationType, VoiceMailAvailableType, VoiceMailClaimType,
		VoiceMailClaimedType, VoiceMailPlaybackResultType, VoiceMailConsumedType,
		VoiceMailExpiredType, PairingSessionCreateType, PairingSessionCreatedType,
		PairingConfirmationType, PairingSucceededType, PairingRejectedType,
		PairingExpiredType,
	}
	payloads := goldenPayloads()
	if len(payloads) != len(types) {
		t.Fatalf("payload count = %d, want %d", len(payloads), len(types))
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	fixturePath := filepath.Clean(filepath.Join(filepath.Dir(source), "../../../testdata/protocol/v2/golden_envelopes.ndjson"))
	fixture, err := os.Open(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()

	scanner := bufio.NewScanner(fixture)
	index := 0
	for scanner.Scan() {
		if index >= len(types) {
			t.Fatal("fixture has more vectors than the canonical type list")
		}
		expected := bytes.TrimSpace(scanner.Bytes())
		wire, err := Encode(types[index], Metadata{MessageID: "golden"}, payloads[index])
		if err != nil {
			t.Fatalf("encode %s typed payload: %v", types[index], err)
		}
		if !bytes.Equal(wire, expected) {
			t.Fatalf("golden vector %d (%s) drifted:\n got: %s\nwant: %s", index, types[index], wire, expected)
		}
		decoded, err := Decode(expected)
		if err != nil {
			t.Fatalf("decode golden %s: %v", types[index], err)
		}
		if decoded.Type != types[index] || decoded.MessageID != "golden" {
			t.Fatalf("decoded golden %d = %+v", index, decoded)
		}
		index++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(types) {
		t.Fatalf("fixture contains %d vectors, want %d", index, len(types))
	}
}
