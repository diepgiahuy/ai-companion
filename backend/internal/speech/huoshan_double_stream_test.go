package speech

import (
	"bytes"
	"testing"
)

func TestHuoshanClientPacketRoundTripPrimitives(t *testing.T) {
	packet, err := buildHuoshanClientPacket(huoshanEventTaskRequest, "session-1", []byte(`{"text":"xin chào"}`))
	if err != nil { t.Fatal(err) }
	if len(packet) < 12 { t.Fatalf("packet too short: %d", len(packet)) }
	if packet[0]>>4 != huoshanProtocolVersion || packet[1]>>4 != huoshanFullClientRequest { t.Fatalf("bad header: %x", packet[:4]) }
	if got := int32FromPacket(t, packet[4:8]); got != huoshanEventTaskRequest { t.Fatalf("event=%d", got) }
}

func TestParseHuoshanAudioEvent(t *testing.T) {
	payload := []byte{1,2,3,4}
	raw := []byte{byte((huoshanProtocolVersion<<4)|huoshanHeaderSize), byte((huoshanAudioOnlyResponse<<4)|huoshanFlagWithEvent), 0, 0}
	raw = appendHuoshanInt32(raw, huoshanEventTTSResponse)
	raw = appendHuoshanContent(raw, []byte("session-1"))
	raw = appendHuoshanContent(raw, payload)
	response, err := parseHuoshanResponse(raw); if err != nil { t.Fatal(err) }
	if response.Event != huoshanEventTTSResponse || response.SessionID != "session-1" || !bytes.Equal(response.Payload,payload) { t.Fatalf("response=%+v", response) }
}

func TestHuoshanConfigFailsClosed(t *testing.T) {
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL:"ws://example.com",AppID:"a",AccessToken:"t",ResourceID:"r",Speaker:"s"}); err == nil { t.Fatal("plaintext remote websocket must fail") }
	if _, err := NewHuoshanDoubleStreamTTS(HuoshanDoubleStreamTTSConfig{URL:"wss://example.com"}); err == nil { t.Fatal("missing credentials must fail") }
}

func int32FromPacket(t *testing.T, raw []byte) int32 { t.Helper(); value,_,err:=readHuoshanInt32(raw,0); if err!=nil { t.Fatal(err) }; return value }
