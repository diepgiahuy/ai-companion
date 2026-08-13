package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	"companion-server/internal/store"

	"github.com/coder/websocket"
	opus "gopkg.in/hraban/opus.v2"
)

type testEnvelope struct {
	Type          protocol.MessageType
	MessageID     string
	Version       int
	Transport     string
	SessionID     string
	TurnID        string
	GenerationID  uint64
	State         string
	Mode          string
	Text          string
	ID            string
	Message       string
	FireAt        string
	Reason        string
	Code          string
	Emotion       protocol.UIEmotion
	ToolName      string
	AudioParams   *protocol.AudioParams
	UI            any
	Config        *protocol.RuntimeConfig
	ConfigVersion int64
	Applied       bool
}

var testEnvelopeSequence atomic.Uint64

type testDeviceAuthenticator struct{}

func (testDeviceAuthenticator) AuthenticateDevice(_ context.Context, deviceID, credential string) (domain.Identity, bool, error) {
	if strings.TrimSpace(deviceID) == "" || credential != "test-device-credential" {
		return domain.Identity{DeviceID: deviceID}, false, nil
	}
	return domain.Identity{UserID: "default", DeviceID: deviceID}, true, nil
}

func newAuthenticatedTestServer(components pipeline.Components, options ...Option) *Server {
	options = append(options, WithDeviceAuthenticator(testDeviceAuthenticator{}))
	return New(components, slog.New(slog.NewTextHandler(io.Discard, nil)), options...)
}

func testDeviceDialOptions(deviceID string) *websocket.DialOptions {
	headers := http.Header{}
	headers.Set("Device-Id", deviceID)
	headers.Set("Authorization", "Bearer test-device-credential")
	return &websocket.DialOptions{HTTPHeader: headers}
}

func (m testEnvelope) MarshalJSON() ([]byte, error) {
	messageID := m.MessageID
	if messageID == "" {
		messageID = "test-" + fmt.Sprint(testEnvelopeSequence.Add(1))
	}
	metadata := protocol.Metadata{
		MessageID: messageID,
		SessionID: m.SessionID,
		TurnID:    m.TurnID,
	}
	switch m.Type {
	case protocol.SessionHelloType:
		if m.AudioParams == nil {
			return nil, fmt.Errorf("audio params are required")
		}
		return protocol.Encode(m.Type, metadata, protocol.HelloPayload{Transport: m.Transport, AudioParams: *m.AudioParams})
	case protocol.TurnListenType:
		mode := m.Mode
		if m.State == "start" && mode == "" {
			mode = "manual"
		}
		return protocol.Encode(m.Type, metadata, protocol.ListenPayload{State: m.State, Mode: mode})
	case protocol.TurnAbortType:
		reason := m.Reason
		if reason == "" {
			reason = "client_abort"
		}
		return protocol.Encode(m.Type, metadata, protocol.AbortPayload{Reason: reason})
	case protocol.AlarmAckType:
		return protocol.Encode(m.Type, metadata, protocol.AlarmAckPayload{AlarmID: m.ID})
	case protocol.ConfigReportType:
		if m.Config == nil {
			return nil, fmt.Errorf("config is required")
		}
		return protocol.Encode(m.Type, metadata, protocol.ConfigReportPayload{ConfigVersion: m.ConfigVersion, Applied: m.Applied, Config: *m.Config})
	case protocol.SessionPingType:
		return protocol.Encode(m.Type, metadata, protocol.EmptyPayload{})
	default:
		return nil, fmt.Errorf("unsupported test input type %q", m.Type)
	}
}

func (m *testEnvelope) UnmarshalJSON(data []byte) error {
	envelope, err := protocol.Decode(data)
	if err != nil {
		return err
	}
	m.Type = envelope.Type
	m.Version = int(envelope.Version)
	m.SessionID = envelope.SessionID
	m.TurnID = envelope.TurnID
	m.GenerationID = envelope.GenerationID
	switch envelope.Type {
	case protocol.SessionReadyType:
		payload, err := protocol.DecodePayload[protocol.ReadyPayload](envelope)
		if err != nil {
			return err
		}
		m.Transport, m.AudioParams = payload.Transport, &payload.AudioParams
		m.Config, m.ConfigVersion = payload.Config, payload.ConfigVersion
	case protocol.TranscriptFinalType:
		payload, err := protocol.DecodePayload[protocol.TextPayload](envelope)
		if err != nil {
			return err
		}
		m.Text = payload.Text
	case protocol.TTSLifecycleType:
		payload, err := protocol.DecodePayload[protocol.TTSLifecyclePayload](envelope)
		if err != nil {
			return err
		}
		m.State, m.Text = payload.State, payload.Text
	case protocol.TurnStateType:
		payload, err := protocol.DecodePayload[protocol.TurnStatePayload](envelope)
		if err != nil {
			return err
		}
		m.State, m.Reason = payload.State, payload.Reason
	case protocol.AlarmFiredType:
		payload, err := protocol.DecodePayload[protocol.AlarmFiredPayload](envelope)
		if err != nil {
			return err
		}
		m.ID, m.Message, m.FireAt = payload.AlarmID, payload.Message, payload.FireAt
	case protocol.ScheduleUpdatedType:
		payload, err := protocol.DecodePayload[protocol.ScheduleUpdatedPayload](envelope)
		if err != nil {
			return err
		}
		m.Message, m.FireAt = payload.Message, payload.FireAt
	case protocol.UICardType:
		payload, err := protocol.DecodePayload[protocol.UICardPayload](envelope)
		if err != nil {
			return err
		}
		m.UI = payload.UI
	case protocol.UIStateType:
		payload, err := protocol.DecodePayload[protocol.UIStatePayload](envelope)
		if err != nil {
			return err
		}
		m.Emotion, m.ToolName = payload.Emotion, payload.ToolName
	case protocol.AgentStatusType:
		payload, err := protocol.DecodePayload[protocol.AgentStatusPayload](envelope)
		if err != nil {
			return err
		}
		m.State = payload.State
	case protocol.ConfigUpdateType:
		payload, err := protocol.DecodePayload[protocol.ConfigUpdatePayload](envelope)
		if err != nil {
			return err
		}
		m.Config, m.ConfigVersion = &payload.Config, payload.ConfigVersion
	case protocol.ProtocolErrorType:
		payload, err := protocol.DecodePayload[protocol.ProtocolErrorPayload](envelope)
		if err != nil {
			return err
		}
		m.Code, m.Message = payload.Code, payload.Message
	case protocol.SessionPongType:
		_, err = protocol.DecodePayload[protocol.EmptyPayload](envelope)
		return err
	default:
		return fmt.Errorf("unsupported test output type %q", envelope.Type)
	}
	return nil
}

func TestDeviceConversationStreamsAudio(t *testing.T) {
	service := newAuthenticatedTestServer(pipeline.Components{
		ASR:    pipeline.MockASR{Transcript: "hôm nay đi chợ 50k"},
		Agent:  pipeline.MockAgent{Reply: "Đã lưu năm mươi nghìn."},
		TTS:    pipeline.MockTTS{Frames: 3},
		Codecs: pipeline.OpusFactory{},
	})
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v2/device"
	connection, _, err := websocket.Dial(ctx, url, testDeviceDialOptions("device-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{
		Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport,
		AudioParams: &audio,
	})
	hello := readJSON(t, ctx, connection)
	if hello.Type != protocol.SessionReadyType || hello.SessionID == "" {
		t.Fatalf("invalid hello response: %+v", hello)
	}

	turnID := "turn-e2e-1"
	writeJSON(t, ctx, connection, testEnvelope{
		Type: protocol.TurnListenType, State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID,
	})
	uplinkEncoder, err := opus.NewEncoder(protocol.UplinkSampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	uplink := make([]byte, protocol.MaximumOpusPacketBytes)
	n, err := uplinkEncoder.Encode(make([]int16, protocol.UplinkSamplesPerFrame), uplink)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageBinary, uplink[:n]); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, ctx, connection, testEnvelope{
		Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: turnID,
	})

	gotSTT := false
	gotStart := false
	gotStop := false
	binaryFrames := 0
	downlinkDecoder, err := opus.NewDecoder(protocol.DownlinkSampleRate, 1)
	if err != nil {
		t.Fatal(err)
	}
	for !gotStop {
		kind, data, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.MessageBinary {
			decoded := make([]int16, protocol.DownlinkSamplesPerFrame)
			decodedSamples, err := downlinkDecoder.Decode(data, decoded)
			if err != nil || decodedSamples != protocol.DownlinkSamplesPerFrame {
				t.Fatalf("invalid downlink Opus: samples=%d error=%v", decodedSamples, err)
			}
			binaryFrames++
			continue
		}
		var message testEnvelope
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		switch {
		case message.Type == protocol.TranscriptFinalType:
			gotSTT = message.Text == "hôm nay đi chợ 50k"
		case message.Type == protocol.TTSLifecycleType && message.State == "start":
			gotStart = true
		case message.Type == protocol.TTSLifecycleType && message.State == "stop":
			gotStop = true
		}
	}
	if !gotSTT || !gotStart || binaryFrames != 3 {
		t.Fatalf("incomplete stream: stt=%v start=%v frames=%d", gotSTT, gotStart, binaryFrames)
	}
}

func writeJSON(t *testing.T, ctx context.Context, connection *websocket.Conn, message testEnvelope) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, ctx context.Context, connection *websocket.Conn) testEnvelope {
	t.Helper()
	kind, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("expected text, got %v", kind)
	}
	var message testEnvelope
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	return message
}

func TestLegacyClientFailsFastWithoutRegisteringSession(t *testing.T) {
	service := newAuthenticatedTestServer(pipeline.Components{
		ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{},
	})
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("legacy-device"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()

	legacy := []byte(`{"type":"hello","version":1,"transport":"websocket","audio_params":{"format":"opus","sample_rate":16000,"channels":1,"frame_duration":60}}`)
	if err := connection.Write(ctx, websocket.MessageText, legacy); err != nil {
		t.Fatal(err)
	}
	kind, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("response kind = %v, want text", kind)
	}
	envelope, err := protocol.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Type != protocol.ProtocolErrorType {
		t.Fatalf("response type = %q", envelope.Type)
	}
	payload, err := protocol.DecodePayload[protocol.ProtocolErrorPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Code != protocol.UnsupportedProtocolVersionCode {
		t.Fatalf("error code = %q", payload.Code)
	}
	if service.hub.get("legacy-device") != nil {
		t.Fatal("legacy client mutated the session hub")
	}
}

func TestDuplicateCommandIsIdempotentAndConflictingReuseFails(t *testing.T) {
	var acknowledgements atomic.Int32
	s := &session{
		id:          "session-1",
		seenInbound: make(map[string]inboundRecord),
		ackReminder: func(context.Context, int64) error {
			acknowledgements.Add(1)
			return nil
		},
	}
	wire, err := protocol.Encode(protocol.AlarmAckType, protocol.Metadata{
		MessageID: "command-1", SessionID: s.id,
	}, protocol.AlarmAckPayload{AlarmID: "reminder-42"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handleControl(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	reordered := []byte(`{"payload":{"alarm_id":"reminder-42"},"session_id":"session-1","message_id":"command-1","type":"alarm.ack","version":2}`)
	if err := s.handleControl(context.Background(), reordered); err != nil {
		t.Fatal(err)
	}
	if got := acknowledgements.Load(); got != 1 {
		t.Fatalf("acknowledgements = %d, want 1", got)
	}

	conflict, err := protocol.Encode(protocol.AlarmAckType, protocol.Metadata{
		MessageID: "command-1", SessionID: s.id,
	}, protocol.AlarmAckPayload{AlarmID: "reminder-43"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.handleControl(context.Background(), conflict); err == nil || !strings.Contains(err.Error(), "reused with different content") {
		t.Fatalf("conflicting message ID error = %v", err)
	}
	if got := acknowledgements.Load(); got != 1 {
		t.Fatalf("conflicting command mutated state: acknowledgements = %d", got)
	}
}

func TestKnownTypeInWrongDirectionIsInvalidEnvelope(t *testing.T) {
	s := &session{id: "session-1", seenInbound: make(map[string]inboundRecord)}
	wire, err := protocol.Encode(protocol.AgentStatusType, protocol.Metadata{
		MessageID: "server-only-1", SessionID: s.id,
	}, protocol.AgentStatusPayload{State: "thinking"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.handleControl(context.Background(), wire)
	if got := protocol.ErrorCode(err); got != protocol.InvalidEnvelopeCode {
		t.Fatalf("wrong-direction error code = %q, want %q: %v", got, protocol.InvalidEnvelopeCode, err)
	}
}

type ackSignalRepo struct {
	SchedulerRepository
	acked chan struct{}
}

func (r *ackSignalRepo) AcknowledgeReminder(ctx context.Context, userID, deviceID string, id int64) error {
	if err := r.SchedulerRepository.AcknowledgeReminder(ctx, userID, deviceID, id); err != nil {
		return err
	}
	select {
	case r.acked <- struct{}{}:
	default:
	}
	return nil
}

func TestReminderSchedulerPushesAlarmToTargetDevice(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "alarm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.CreateReminderForDevice(context.Background(), "default", "alarm-1", "device-test", "Hết giờ 30 phút", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	repo := &ackSignalRepo{SchedulerRepository: data, acked: make(chan struct{}, 1)}
	service := newAuthenticatedTestServer(pipeline.Components{
		ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{},
	}, WithStore(repo), WithSchedulerInterval(20*time.Millisecond))
	background, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	go service.RunBackground(background)

	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v2/device"
	connection, _, err := websocket.Dial(ctx, url, testDeviceDialOptions("device-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")
	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	_ = readJSON(t, ctx, connection)

	var alarm testEnvelope
	for alarm.Type != protocol.AlarmFiredType {
		kind, payload, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText {
			continue
		}
		if err := json.Unmarshal(payload, &alarm); err != nil {
			t.Fatal(err)
		}
	}
	if alarm.Message != "Het gio 30 phut" || alarm.ID == "" {
		t.Fatalf("alarm = %+v", alarm)
	}
	writeJSON(t, ctx, connection, testEnvelope{Type: protocol.AlarmAckType, SessionID: alarm.SessionID, ID: alarm.ID})
	select {
	case <-repo.acked:
	case <-ctx.Done():
		t.Fatalf("alarm acknowledgement was not committed: %v", ctx.Err())
	}
	fired, err := data.ListReminders(context.Background(), "default", "device-test", "fired", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(fired) != 1 {
		t.Fatalf("fired reminders = %+v", fired)
	}
}

func TestOLEDTextDegradesVietnameseToASCII(t *testing.T) {
	if got := oledText("15:00 Họp team - nhắc uống nước"); got != "15:00 Hop team - nhac uong nuoc" {
		t.Fatalf("oledText = %q", got)
	}
}

func TestOTAManifestPublishAndDeviceCompatibility(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ota.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	firmware := controlplane.NewFirmware(data, nil, false)
	service := newAuthenticatedTestServer(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, WithFirmwareService(firmware), WithAdminToken("admin-token"))
	ts := httptest.NewServer(service.Handler())
	defer ts.Close()
	manifest := controlplane.FirmwareManifest{Version: "1.2.3", Channel: "stable", Board: "esp32-s3-devkitc-1", ProtocolMin: 1, SecurityVersion: 2, URL: "https://firmware.example/1.2.3.bin", SHA256: strings.Repeat("ab", 32), Size: 1024, ExpiresAt: time.Now().Add(time.Hour), MetadataVersion: 7}
	body, _ := json.Marshal(manifest)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/firmware", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish status=%d", resp.StatusCode)
	}

	get, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/ota?board=esp32-s3-devkitc-1&channel=stable&protocol=3&security_version=1&metadata_version=0", nil)
	get.Header.Set("Device-Id", "ota-device")
	get.Header.Set("Authorization", "Bearer test-device-credential")
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ota status=%d", resp.StatusCode)
	}
	var got controlplane.FirmwareManifest
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.2.3" || got.MetadataVersion != 7 {
		t.Fatalf("manifest=%+v", got)
	}

	get2, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/ota?board=esp32-s3-devkitc-1&channel=stable&protocol=3&security_version=2&metadata_version=7", nil)
	get2.Header.Set("Device-Id", "ota-device")
	get2.Header.Set("Authorization", "Bearer test-device-credential")
	resp2, err := http.DefaultClient.Do(get2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("same metadata should be no-content, got=%d", resp2.StatusCode)
	}
}

type blockingStreamAgent struct {
	continueCh chan struct{}
}

func (a *blockingStreamAgent) Respond(context.Context, string, string) (string, error) {
	return "unused", nil
}

func (a *blockingStreamAgent) Stream(ctx context.Context, _, _ string, emit func(pipeline.AgentStreamEvent) error) error {
	if err := emit(pipeline.AgentStreamEvent{TextDelta: "Xin chào bạn,"}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.continueCh:
	}
	return emit(pipeline.AgentStreamEvent{TextDelta: " hôm nay thế nào?"})
}

func TestStreamingAgentStartsTTSBeforeModelFinishes(t *testing.T) {
	agentRuntime := &blockingStreamAgent{continueCh: make(chan struct{})}
	service := newAuthenticatedTestServer(pipeline.Components{
		ASR:    pipeline.MockASR{Transcript: "hello"},
		Agent:  agentRuntime,
		TTS:    pipeline.MockTTS{Frames: 1},
		Codecs: pipeline.OpusFactory{},
	})
	ts := httptest.NewServer(service.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v2/device", testDeviceDialOptions("stream-device"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, conn, testEnvelope{Type: protocol.SessionHelloType, Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, conn)
	turnID := "stream-before-finish"
	writeJSON(t, ctx, conn, testEnvelope{Type: protocol.TurnListenType, State: "start", SessionID: hello.SessionID, TurnID: turnID})
	encoder, err := opus.NewEncoder(protocol.UplinkSampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, protocol.MaximumOpusPacketBytes)
	n, err := encoder.Encode(make([]int16, protocol.UplinkSamplesPerFrame), packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, packet[:n]); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, ctx, conn, testEnvelope{Type: protocol.TurnListenType, State: "stop", SessionID: hello.SessionID, TurnID: turnID})

	firstSentence := false
	firstAudio := false
	continued := false
	gotStop := false
	for !gotStop {
		kind, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.MessageBinary {
			firstAudio = true
			continue
		}
		var msg testEnvelope
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type == protocol.TTSLifecycleType && msg.State == "sentence_start" && msg.Text == "Xin chào bạn," {
			firstSentence = true
			// If the server had waited for the full model response, this message
			// could never arrive because the model is blocked on continueCh.
			if !continued {
				close(agentRuntime.continueCh)
				continued = true
			}
		}
		if msg.Type == protocol.TTSLifecycleType && msg.State == "stop" {
			gotStop = true
		}
	}
	if !firstSentence || !firstAudio || !continued {
		t.Fatalf("streaming path did not overlap model/TTS: sentence=%v audio=%v continued=%v", firstSentence, firstAudio, continued)
	}
}

func TestTurnGenerationInvalidatesQueuedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &session{
		id:            "session-1",
		controlWrites: make(chan outbound, 4),
		mediaWrites:   make(chan outbound, 4),
		generation:    7,
	}
	turnCtx, turnCancel := context.WithCancel(ctx)
	current := &turn{id: "turn-7", ctx: turnCtx, cancel: turnCancel, generation: 7, state: "speaking"}
	s.active = current
	old := outbound{kind: websocket.MessageBinary, data: []byte{1}, generation: 7, turnScoped: true}
	if !s.outboundCurrent(old) {
		t.Fatal("current generation was unexpectedly stale")
	}

	s.interruptActive(ctx, "test")
	if s.outboundCurrent(old) {
		t.Fatal("stale queued audio remained writable after interruption")
	}
	select {
	case terminal := <-s.controlWrites:
		if terminal.turnScoped {
			t.Fatal("interrupt terminal event must not be generation-scoped")
		}
	default:
		t.Fatal("expected interruption control event")
	}
}

func TestTurnMediaLifecycleSharesFIFOWithAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := &session{
		controlWrites: make(chan outbound, 4),
		mediaWrites:   make(chan outbound, 4),
		generation:    3,
	}
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	current := &turn{id: "turn-3", ctx: turnCtx, cancel: turnCancel, generation: 3}
	s.active = current

	if err := s.sendTurnMediaJSON(ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "start"}); err != nil {
		t.Fatal(err)
	}
	if err := s.sendTurn(ctx, current, outbound{kind: websocket.MessageBinary, data: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.sendTurnMediaJSON(ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "stop"}); err != nil {
		t.Fatal(err)
	}

	startMessage := <-s.mediaWrites
	if startMessage.kind != websocket.MessageText {
		t.Fatalf("first media item kind=%v, want text lifecycle event", startMessage.kind)
	}
	var startControl testEnvelope
	if err := json.Unmarshal(startMessage.data, &startControl); err != nil {
		t.Fatal(err)
	}
	if startControl.Type != protocol.TTSLifecycleType || startControl.State != "start" {
		t.Fatalf("first media control=%+v, want tts start", startControl)
	}

	audio := <-s.mediaWrites
	if audio.kind != websocket.MessageBinary || len(audio.data) != 1 {
		t.Fatalf("second media item=%#v, want audio", audio)
	}

	stopMessage := <-s.mediaWrites
	if stopMessage.kind != websocket.MessageText {
		t.Fatalf("third media item kind=%v, want text lifecycle event", stopMessage.kind)
	}
	var stopControl testEnvelope
	if err := json.Unmarshal(stopMessage.data, &stopControl); err != nil {
		t.Fatal(err)
	}
	if stopControl.Type != protocol.TTSLifecycleType || stopControl.State != "stop" {
		t.Fatalf("third media control=%+v, want tts stop", stopControl)
	}
}

func TestTurnMediaControlWaitsForTransientQueueCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := &session{mediaWrites: make(chan outbound, 1), generation: 5}
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	current := &turn{id: "turn-5", ctx: turnCtx, cancel: turnCancel, generation: 5}
	s.active = current

	// Fill the lane with the final accepted audio frame. A causally-dependent
	// stop event may wait for bounded capacity rather than failing immediately.
	s.mediaWrites <- outbound{kind: websocket.MessageBinary, data: []byte{1}, turnScoped: true, generation: 5}
	done := make(chan error, 1)
	go func() {
		done <- s.sendTurnMediaJSON(ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "stop"})
	}()

	select {
	case err := <-done:
		t.Fatalf("media control returned while lane was transiently full: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	<-s.mediaWrites // writer makes one slot available
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	queued := <-s.mediaWrites
	if queued.kind != websocket.MessageText {
		t.Fatalf("queued item kind=%v, want media control text", queued.kind)
	}
}
