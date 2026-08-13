package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var interactionTestTime = time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC)

func interactionEnvelope(kind InteractionType) Envelope {
	return Envelope{
		Type:           kind,
		Version:        Version,
		MessageID:      "event-001",
		IdempotencyKey: "idem-001",
		OccurredAt:     interactionTestTime.Format(time.RFC3339),
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
			if gotEnvelope.Type != tc.kind || gotEnvelope.Version != Version {
				t.Fatalf("decoded envelope = %+v", gotEnvelope)
			}
			if !reflect.DeepEqual(reflect.ValueOf(tc.body).Interface(), reflect.ValueOf(gotBody).Elem().Interface()) {
				t.Fatalf("decoded payload = %#v, want %#v", gotBody, tc.body)
			}
		})
	}
}

func TestDecodeInteractionUsesEnvelopeV2ErrorCodes(t *testing.T) {
	cases := []struct {
		name     string
		wire     string
		wantCode string
	}{
		{"v1 flat message", `{"version":1,"type":"gesture.notification","id":"event-001","gesture":"pat"}`, UnsupportedProtocolVersionCode},
		{"unknown type", `{"version":2,"type":"future.event","message_id":"event-001","idempotency_key":"idem-001","occurred_at":"2026-08-13T10:00:00Z","payload":{}}`, UnknownMessageTypeCode},
		{"non-interaction type", `{"version":2,"type":"session.ping","message_id":"event-001","idempotency_key":"idem-001","occurred_at":"2026-08-13T10:00:00Z","payload":{}}`, UnknownMessageTypeCode},
		{"malformed JSON", `{"version":2,"type":`, InvalidEnvelopeCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeInteraction([]byte(tc.wire))
			if err == nil {
				t.Fatal("DecodeInteraction() succeeded")
			}
			if got := ErrorCode(err); got != tc.wantCode {
				t.Fatalf("ErrorCode(%v) = %q, want %q", err, got, tc.wantCode)
			}
		})
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
	if got := ErrorCode(err); got != InvalidEnvelopeCode {
		t.Fatalf("ErrorCode(%v) = %q, want %q", err, got, InvalidEnvelopeCode)
	}

	_, err = EncodeInteraction(interactionEnvelope(PairingConfirmationType), PairingConfirmation{
		SessionID: "session-1", Participant: testParticipant("owner-a", "device-a"),
		ConfirmationNonce: "short", ConfirmedAt: interactionTestTime,
	})
	if err == nil {
		t.Fatal("EncodeInteraction() succeeded for short confirmation nonce")
	}
}

func TestDecodeInteractionRejectsBadPayloadAndMetadata(t *testing.T) {
	valid := interactionEnvelope(GestureNotificationType)
	valid.Payload = json.RawMessage(`{"gesture":"pat","sender_device_id":"device-a"}`)

	cases := []struct {
		name     string
		mutate   func(*Envelope)
		wantCode string
	}{
		{
			name: "bad checksum",
			mutate: func(e *Envelope) {
				e.Type = VoiceMailAvailableType
				e.Payload = json.RawMessage(`{"voice_mail_id":"voice-1","from_device_id":"device-a","media_format":"ogg_opus","duration_ms":1000,"size_bytes":2000,"checksum_sha256":"not-a-checksum","expires_at":"2026-08-13T11:00:00Z","policy":"ephemeral"}`)
			},
			wantCode: InvalidEnvelopeCode,
		},
		{
			name: "unknown payload field",
			mutate: func(e *Envelope) {
				e.Payload = json.RawMessage(`{"gesture":"pat","sender_device_id":"device-a","future_hint":"reject"}`)
			},
			wantCode: InvalidEnvelopeCode,
		},
		{
			name: "missing idempotency key",
			mutate: func(e *Envelope) {
				e.IdempotencyKey = ""
			},
			wantCode: InvalidEnvelopeCode,
		},
		{
			name: "missing message id",
			mutate: func(e *Envelope) {
				e.MessageID = ""
			},
			wantCode: InvalidEnvelopeCode,
		},
		{
			name: "non RFC3339 occurred at",
			mutate: func(e *Envelope) {
				e.OccurredAt = "13-08-2026"
			},
			wantCode: InvalidEnvelopeCode,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := valid
			tc.mutate(&e)
			wire, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = DecodeInteraction(wire)
			if err == nil {
				t.Fatal("DecodeInteraction() succeeded")
			}
			if got := ErrorCode(err); got != tc.wantCode {
				t.Fatalf("ErrorCode(%v) = %q, want %q", err, got, tc.wantCode)
			}
		})
	}
}

func TestEncodeInteractionRejectsNonV2Envelope(t *testing.T) {
	e := interactionEnvelope(GestureNotificationType)
	e.Version = 1
	_, err := EncodeInteraction(e, GestureNotification{Gesture: "pat", SenderDeviceID: "device-a"})
	if got := ErrorCode(err); got != UnsupportedProtocolVersionCode {
		t.Fatalf("ErrorCode(%v) = %q, want %q", err, got, UnsupportedProtocolVersionCode)
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
