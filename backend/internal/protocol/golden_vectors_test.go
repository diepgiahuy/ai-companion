package protocol

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGoldenEnvelopeVectorsCoverEveryMessageType(t *testing.T) {
	types := []MessageType{
		SessionHelloType, SessionReadyType, SessionPingType, SessionPongType,
		TurnListenType, TurnAbortType, TurnStateType, TranscriptFinalType,
		TTSLifecycleType, AgentStatusType, UICardType, UIStateType,
		AlarmFiredType, AlarmAckType, ScheduleUpdatedType, ConfigUpdateType,
		ConfigReportType, ProtocolErrorType,
		GestureNotificationType, VoiceMailAvailableType, VoiceMailClaimType,
		VoiceMailClaimedType, VoiceMailPlaybackResultType, VoiceMailConsumedType,
		VoiceMailExpiredType, PairingSessionCreateType, PairingSessionCreatedType,
		PairingConfirmationType, PairingSucceededType, PairingRejectedType,
		PairingExpiredType,
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
			t.Fatalf("fixture has more vectors than the canonical type list")
		}
		expected := bytes.TrimSpace(scanner.Bytes())
		wire, err := Encode(types[index], Metadata{MessageID: "golden"}, EmptyPayload{})
		if err != nil {
			t.Fatalf("encode %s: %v", types[index], err)
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
