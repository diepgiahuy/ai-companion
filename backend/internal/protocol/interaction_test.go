package protocol

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

var interactionTestTime = time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)

func interactionEnvelope(kind InteractionType) InteractionEnvelope {
	return InteractionEnvelope{
		Type:           kind,
		Version:        InteractionVersion,
		ID:             "event-001",
		IdempotencyKey: "idem-001",
		OccurredAt:     interactionTestTime,
	}
}

func testParticipant(owner, device string) PairingParticipant {
	return PairingParticipant{OwnerUserID: owner, DeviceID: device}
}

func TestInteractionRoundTripForEveryPayload(t *testing.T) {
	initiator := testParticipant("owner-a", "device-a")
	peer := testParticipant("owner-b", "device-b")
	expires := interactionTestTime.Add(time.Hour)
	cases := []struct {
		name string
		kind InteractionType
		body InteractionPayload
	}{
		{"gesture", GestureNotificationType, GestureNotification{Gesture: "pat", SenderDeviceID: "device-a"}},
		{"voice available", VoiceMailAvailableType, VoiceMailAvailable{VoiceMailID: "voice-1", FromDeviceID: "device-a", MediaFormat: "ogg_opus", DurationMS: 1000, SizeBytes: 2000, ChecksumSHA256: strings.Repeat("a", 64), ExpiresAt: expires, Policy: VoiceMailPolicyEphemeral}},
		{"voice claim", VoiceMailClaimType, VoiceMailClaim{VoiceMailID: "voice-1", PlaybackID: "play-1"}},
		{"voice claimed", VoiceMailClaimedType, VoiceMailClaimed{VoiceMailID: "voice-1", PlaybackID: "play-1", MediaRef: "media-ref-1", LeaseExpiresAt: expires}},
		{"voice playback result", VoiceMailPlaybackResultType, VoiceMailPlaybackResult{VoiceMailID: "voice-1", PlaybackID: "play-1", Result: PlaybackSucceeded}},
		{"voice consumed", VoiceMailConsumedType, VoiceMailConsumed{VoiceMailID: "voice-1", PlaybackID: "play-1"}},
		{"voice expired", VoiceMailExpiredType, VoiceMailExpired{VoiceMailID: "voice-1"}},
		{"pairing create", PairingSessionCreateType, PairingSessionCreate{Initiator: initiator, CandidateDeviceID: "device-b", ProximityEvidenceID: "observation-1"}},
		{"pairing created", PairingSessionCreatedType, PairingSessionCreated{SessionID: "session-1", Initiator: initiator, Peer: peer, ExpiresAt: expires}},
		{"pairing confirmation", PairingConfirmationType, PairingConfirmation{SessionID: "session-1", Participant: initiator, ConfirmationNonce: "0123456789abcdef", ConfirmedAt: interactionTestTime}},
		{"pairing succeeded", PairingSucceededType, PairingSucceeded{SessionID: "session-1", RelationshipID: "relationship-1", Initiator: initiator, Peer: peer}},
		{"pairing rejected", PairingRejectedType, PairingRejected{SessionID: "session-1", Reason: "user_declined"}},
		{"pairing expired", PairingExpiredType, PairingExpired{SessionID: "session-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := EncodeInteraction(interactionEnvelope(tc.kind), tc.body)
			if err != nil {
				t.Fatalf("EncodeInteraction() error = %v", err)
			}
			gotEnvelope, gotBody, err := DecodeInteraction(wire)
			if err != nil {
				t.Fatalf("DecodeInteraction() error = %v", err)
			}
			if gotEnvelope.Type != tc.kind || gotEnvelope.Version != InteractionVersion {
				t.Fatalf("decoded envelope = %+v", gotEnvelope)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.body).Interface(), reflect.ValueOf(gotBody).Elem().Interface()) {
				t.Fatalf("decoded payload = %#v, want %#v", gotBody, tc.body)
			}
		})
	}
}

func TestDecodeInteractionRejectsUnknownAndUnsupportedVersions(t *testing.T) {
	cases := []struct {
		name string
		wire string
		want error
	}{
		{"unknown type", `{"type":"future.event","version":1,"id":"event-001","idempotency_key":"idem-001","occurred_at":"2026-08-13T10:00:00Z","payload":{}}`, ErrUnknownInteractionType},
		{"unsupported version", `{"type":"gesture.notification","version":2,"id":"event-001","idempotency_key":"idem-001","occurred_at":"2026-08-13T10:00:00Z","payload":{}}`, ErrUnsupportedInteractionVersion},
		{"malformed JSON", `{"type":`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeInteraction([]byte(tc.wire))
			if err == nil {
				t.Fatal("DecodeInteraction() succeeded")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeInteractionAllowsFutureOptionalPayloadFields(t *testing.T) {
	wire := `{"type":"gesture.notification","version":1,"id":"event-001","idempotency_key":"idem-001","occurred_at":"2026-08-13T10:00:00Z","payload":{"gesture":"pat","sender_device_id":"device-a","future_hint":"ignore"}}`
	_, body, err := DecodeInteraction([]byte(wire))
	if err != nil {
		t.Fatalf("DecodeInteraction() error = %v", err)
	}
	if got := body.(*GestureNotification).Gesture; got != "pat" {
		t.Fatalf("gesture = %q, want pat", got)
	}
}

func TestInteractionValidationRejectsMalformedPayload(t *testing.T) {
	_, err := EncodeInteraction(interactionEnvelope(VoiceMailAvailableType), VoiceMailAvailable{
		VoiceMailID: "voice-1", FromDeviceID: "device-a", MediaFormat: "ogg_opus",
		DurationMS: 0, SizeBytes: 1, ChecksumSHA256: strings.Repeat("a", 64),
		ExpiresAt: interactionTestTime.Add(time.Hour), Policy: VoiceMailPolicyEphemeral,
	})
	if err == nil {
		t.Fatal("EncodeInteraction() succeeded for invalid duration")
	}

	_, err = EncodeInteraction(interactionEnvelope(PairingConfirmationType), PairingConfirmation{
		SessionID: "session-1", Participant: testParticipant("owner-a", "device-a"),
		ConfirmationNonce: "short", ConfirmedAt: interactionTestTime,
	})
	if err == nil {
		t.Fatal("EncodeInteraction() succeeded for short confirmation nonce")
	}
}

func TestLegacyMessageRemainsCompatible(t *testing.T) {
	before := Message{Type: "ui_state", Emotion: UIEmotionIdle}
	wire, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after Message
	if err := json.Unmarshal(wire, &after); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUIState(after); err != nil {
		t.Fatalf("ValidateUIState() error = %v", err)
	}
	if strings.Contains(string(wire), "payload") || !reflect.DeepEqual(before, after) {
		t.Fatalf("legacy wire changed: %s", wire)
	}
}

func TestVoiceMailStateTransitions(t *testing.T) {
	cases := []struct {
		name   string
		state  VoiceMailState
		event  VoiceMailEvent
		policy VoiceMailPolicy
		want   VoiceMailState
	}{
		{"claim", VoiceMailAvailableState, VoiceMailClaimEvent, VoiceMailPolicyEphemeral, VoiceMailClaimedState},
		{"ephemeral success consumes", VoiceMailClaimedState, VoiceMailPlaybackSucceededEvent, VoiceMailPolicyEphemeral, VoiceMailConsumedState},
		{"retained success returns available", VoiceMailClaimedState, VoiceMailPlaybackSucceededEvent, VoiceMailPolicyRetained, VoiceMailAvailableState},
		{"failed playback retries", VoiceMailClaimedState, VoiceMailPlaybackFailedEvent, VoiceMailPolicyEphemeral, VoiceMailAvailableState},
		{"lease expiry retries", VoiceMailClaimedState, VoiceMailLeaseExpiredEvent, VoiceMailPolicyEphemeral, VoiceMailAvailableState},
		{"expiry", VoiceMailAvailableState, VoiceMailExpireEvent, VoiceMailPolicyRetained, VoiceMailExpiredState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextVoiceMailState(tc.state, tc.event, tc.policy)
			if err != nil || got != tc.want {
				t.Fatalf("NextVoiceMailState() = %q, %v; want %q, nil", got, err, tc.want)
			}
		})
	}
	if _, err := NextVoiceMailState(VoiceMailClaimedState, VoiceMailClaimEvent, VoiceMailPolicyEphemeral); err == nil {
		t.Fatal("duplicate claim was accepted")
	}
	if _, err := NextVoiceMailState(VoiceMailConsumedState, VoiceMailExpireEvent, VoiceMailPolicyEphemeral); err == nil {
		t.Fatal("terminal transition was accepted")
	}
}

func TestPairingStateTransitions(t *testing.T) {
	state, err := NextPairingState(PairingAwaitingConfirmation, PairingConfirmInitiatorEvent)
	if err != nil || state != PairingInitiatorConfirmed {
		t.Fatalf("first confirmation = %q, %v", state, err)
	}
	state, err = NextPairingState(state, PairingConfirmPeerEvent)
	if err != nil || state != PairingSucceededState {
		t.Fatalf("second confirmation = %q, %v", state, err)
	}
	if _, err := NextPairingState(PairingInitiatorConfirmed, PairingConfirmInitiatorEvent); err == nil {
		t.Fatal("duplicate confirmation was accepted")
	}
	state, err = NextPairingState(PairingPeerConfirmed, PairingRejectEvent)
	if err != nil || state != PairingRejectedState {
		t.Fatalf("rejection = %q, %v", state, err)
	}
	if _, err := NextPairingState(PairingExpiredState, PairingConfirmPeerEvent); err == nil {
		t.Fatal("terminal pairing transition was accepted")
	}
}
