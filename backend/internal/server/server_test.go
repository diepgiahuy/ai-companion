package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"companion-server/internal/agent"
	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	conversationprovider "companion-server/internal/providers/conversation"
	resourceprovider "companion-server/internal/providers/resources"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/store"

	"github.com/coder/websocket"
	opus "gopkg.in/hraban/opus.v2"
)

func TestDeviceConversationStreamsAudio(t *testing.T) {
	service := New(pipeline.Components{
		ASR:    pipeline.MockASR{Transcript: "hôm nay đi chợ 50k"},
		Agent:  pipeline.MockAgent{Reply: "Đã lưu năm mươi nghìn."},
		TTS:    pipeline.MockTTS{Frames: 3},
		Codecs: pipeline.OpusFactory{},
	}, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/device"
	connection, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, protocol.Message{
		Type: "hello", Version: protocol.Version, Transport: protocol.Transport,
		AudioParams: &audio,
	})
	hello := readJSON(t, ctx, connection)
	if hello.Type != "hello" || hello.SessionID == "" {
		t.Fatalf("invalid hello response: %+v", hello)
	}

	turnID := "turn-e2e-1"
	writeJSON(t, ctx, connection, protocol.Message{
		Type: "listen", State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID,
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
	writeJSON(t, ctx, connection, protocol.Message{
		Type: "listen", State: "stop", SessionID: hello.SessionID, TurnID: turnID,
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
		var message protocol.Message
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		switch {
		case message.Type == "stt":
			gotSTT = message.Text == "hôm nay đi chợ 50k"
		case message.Type == "tts" && message.State == "start":
			gotStart = true
		case message.Type == "tts" && message.State == "stop":
			gotStop = true
		}
	}
	if !gotSTT || !gotStart || binaryFrames != 3 {
		t.Fatalf("incomplete stream: stt=%v start=%v frames=%d", gotSTT, gotStart, binaryFrames)
	}
}

func writeJSON(t *testing.T, ctx context.Context, connection *websocket.Conn, message protocol.Message) {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, ctx context.Context, connection *websocket.Conn) protocol.Message {
	t.Helper()
	kind, data, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("expected text, got %v", kind)
	}
	var message protocol.Message
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatal(err)
	}
	return message
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
	service := New(pipeline.Components{
		ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{},
	}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), WithStore(data), WithSchedulerInterval(20*time.Millisecond))
	background, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	go service.RunBackground(background)

	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/device"
	headers := http.Header{}
	headers.Set("Device-Id", "device-test")
	connection, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")
	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, protocol.Message{Type: "hello", Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	_ = readJSON(t, ctx, connection)

	var alarm protocol.Message
	for alarm.Type != "alarm" {
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
	writeJSON(t, ctx, connection, protocol.Message{Type: "alarm_ack", ID: alarm.ID})
	time.Sleep(20 * time.Millisecond)
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

func TestExpenseBudgetFullE2EThroughQwenRegistryAndUI(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "full-e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	when, _ := time.Parse(time.RFC3339, "2026-08-10T10:00:00+07:00")
	if err := data.CreateExpense(context.Background(), "default", "seed-e2e", 250000, "food", "food", when); err != nil {
		t.Fatal(err)
	}
	if err := data.SetBudget(context.Background(), "default", "weekly", 1000000); err != nil {
		t.Fatal(err)
	}

	var modelRequests atomic.Int32
	modelEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if modelRequests.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{"id": "expense-query-1", "type": "function", "function": map[string]any{
					"name": "expense.query", "arguments": `{"from":"2026-08-10T00:00:00+07:00","to":"2026-08-17T00:00:00+07:00","period":"weekly"}`,
				}}},
			}}}})
			return
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, `"total_vnd":250000`) || !strings.Contains(last.Content, `"remaining_vnd":750000`) {
			t.Fatalf("Qwen second pass missing authoritative tool result: %+v", last)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": "Tuần này bạn đã chi 250 nghìn, còn 750 nghìn.",
		}}}})
	}))
	defer modelEndpoint.Close()

	location, _ := time.LoadLocation("Asia/Ho_Chi_Minh")
	conversationService := conversationctx.New(conversationprovider.NewSQLite(data), conversationctx.NewMemoryCache(30*time.Minute, 10))
	resources := capability.NewResourceRegistry()
	if err := resources.Register(resourceprovider.NewNative(data, conversationService, location)); err != nil {
		t.Fatal(err)
	}
	tools := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(tools, toolprovider.NativeDependencies{Store: data, Resources: resources, RecordingsDir: filepath.Join(t.TempDir(), "recordings")}); err != nil {
		t.Fatal(err)
	}
	qwen, err := agent.NewQwen(modelEndpoint.URL, "", "Qwen3-4B-Instruct-2507", "Asia/Ho_Chi_Minh", data,
		agent.WithConversation(conversationService), agent.WithToolRegistry(tools))
	if err != nil {
		t.Fatal(err)
	}

	service := New(pipeline.Components{
		ASR: pipeline.MockASR{Transcript: "Tuần này tiêu hết bao nhiêu rồi?"}, Agent: qwen,
		TTS: pipeline.MockTTS{Frames: 2}, Codecs: pipeline.OpusFactory{},
	}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), WithStore(data), WithLocation(location))
	httpServer := httptest.NewServer(service.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Device-Id", "device-expense-e2e")
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v1/device", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "test done")

	audio := protocol.DefaultAudioParams()
	writeJSON(t, ctx, connection, protocol.Message{Type: "hello", Version: protocol.Version, Transport: protocol.Transport, AudioParams: &audio})
	hello := readJSON(t, ctx, connection)
	turnID := "turn-expense-e2e"
	writeJSON(t, ctx, connection, protocol.Message{Type: "listen", State: "start", Mode: "manual", SessionID: hello.SessionID, TurnID: turnID})
	encoder, err := opus.NewEncoder(protocol.UplinkSampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, protocol.MaximumOpusPacketBytes)
	n, err := encoder.Encode(make([]int16, protocol.UplinkSamplesPerFrame), packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Write(ctx, websocket.MessageBinary, packet[:n]); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, ctx, connection, protocol.Message{Type: "listen", State: "stop", SessionID: hello.SessionID, TurnID: turnID})

	gotUI, gotAnswer, gotStop := false, false, false
	binaryFrames := 0
	for !gotStop {
		kind, raw, err := connection.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.MessageBinary {
			binaryFrames++
			continue
		}
		var message protocol.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		switch {
		case message.Type == "ui":
			ui, ok := message.UI.(map[string]any)
			progress, progressOK := ui["progress"].(float64)
			gotUI = ok && progressOK && ui["kind"] == "expense_summary" && int(progress) == 25
		case message.Type == "tts" && message.State == "sentence_start":
			gotAnswer = strings.Contains(message.Text, "250 nghìn") && strings.Contains(message.Text, "750 nghìn")
		case message.Type == "tts" && message.State == "stop":
			gotStop = true
		}
	}
	if !gotUI || !gotAnswer || binaryFrames != 2 || modelRequests.Load() != 2 {
		t.Fatalf("full E2E incomplete ui=%v answer=%v frames=%d model_requests=%d", gotUI, gotAnswer, binaryFrames, modelRequests.Load())
	}
	history, err := conversationService.Recent(context.Background(), conversationctx.Scope{UserID: "default", ThreadID: "device-expense-e2e"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Role != "user" || history[1].Role != "assistant" {
		t.Fatalf("conversation history = %+v", history)
	}
}

func TestOTAManifestPublishAndDeviceCompatibility(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ota.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	firmware := controlplane.NewFirmware(data, nil, false)
	service := New(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, "device-token", slog.New(slog.NewTextHandler(io.Discard, nil)), WithFirmwareService(firmware), WithAdminToken("admin-token"))
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
	get.Header.Set("Authorization", "Bearer device-token")
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
	get2.Header.Set("Authorization", "Bearer device-token")
	resp2, err := http.DefaultClient.Do(get2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("same metadata should be no-content, got=%d", resp2.StatusCode)
	}
}
