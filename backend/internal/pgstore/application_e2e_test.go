//go:build apppge2e

package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	toolprovider "companion-server/internal/providers/tools"
	"companion-server/internal/server"

	"github.com/coder/websocket"
)

type postgresApplicationAgent struct {
	registry     *capability.ToolRegistry
	conversation *conversationctx.Service
	key          string
	content      string
	results      chan string
}

func (a *postgresApplicationAgent) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	turn, ok := pipeline.CurrentTurn(ctx)
	if !ok || strings.TrimSpace(turn.UserID) == "" || strings.TrimSpace(turn.DeviceID) == "" {
		return "", fmt.Errorf("missing authenticated turn identity")
	}
	scope := conversationctx.Scope{UserID: turn.UserID, ThreadID: turn.ThreadID}
	if err := a.conversation.Append(ctx, turnID+":user", scope, "user", transcript); err != nil {
		return "", err
	}
	result := a.registry.Execute(ctx, "note.create", capability.ToolRequest{
		Key:       a.key,
		Arguments: fmt.Sprintf(`{"content":%q}`, a.content),
	})
	if err := a.conversation.Append(ctx, turnID+":assistant", scope, "assistant", result.Content); err != nil {
		return "", err
	}
	select {
	case a.results <- result.Content:
	default:
	}
	return result.Content, nil
}

func TestPostgresApplicationServerToolRestartNoSQLite(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_POSTGRES_APP_TEST_DSN"))
	if dsn == "" {
		t.Skip("COMPANION_POSTGRES_APP_TEST_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	data, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	prefix := fmt.Sprintf("app-pg-%d", time.Now().UnixNano())
	userID := prefix + "-user"
	deviceID := prefix + "-device"
	credential := prefix + "-credential-0123456789"
	idempotencyKey := prefix + "-note-key"
	threadID := prefix + "-thread"

	if err := data.EnrollDevice(ctx, domain.Identity{UserID: userID, DeviceID: deviceID, TenantID: "tier1", Plan: "test"}, credential); err != nil {
		t.Fatal(err)
	}

	sqlitePath := filepath.Join(t.TempDir(), "must-not-exist.db")
	t.Setenv("COMPANION_DATABASE", sqlitePath)

	firstConversation := conversationctx.New(data, nil)
	firstRegistry := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(firstRegistry, toolprovider.NativeDependencies{Store: data, Conversation: firstConversation}); err != nil {
		t.Fatal(err)
	}
	firstResults := make(chan string, 1)
	firstAgent := &postgresApplicationAgent{
		registry: firstRegistry, conversation: firstConversation,
		key: idempotencyKey, content: "alpha", results: firstResults,
	}
	firstService := newPostgresApplicationTestServer(data, firstAgent, "first transcript")
	firstHTTP := httptest.NewServer(firstService.Handler())

	assertPostgresApplicationUnauthorized(t, firstHTTP.URL, deviceID, "wrong-credential")
	runPostgresApplicationTurn(t, firstHTTP.URL, deviceID, credential, threadID, "turn-1")
	firstResult := waitPostgresApplicationResult(t, firstResults)
	if !toolResultOK(firstResult) {
		t.Fatalf("first note tool failed: %s", firstResult)
	}
	firstHTTP.Close()

	notes, err := data.ListNotes(ctx, userID, 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "alpha" {
		t.Fatalf("notes after first application instance=%+v err=%v", notes, err)
	}

	// Simulate a process restart by rebuilding all application-facing services and
	// server state while keeping only PostgreSQL durable state. No in-memory cache,
	// server session, ToolRegistry or conversation service is reused.
	secondData, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer secondData.Close()
	secondConversation := conversationctx.New(secondData, nil)
	history, err := secondConversation.Recent(ctx, conversationctx.Scope{UserID: userID, ThreadID: threadID}, 10)
	if err != nil || len(history) != 2 || history[0].Content != "first transcript" {
		t.Fatalf("conversation did not survive application restart: history=%+v err=%v", history, err)
	}

	secondRegistry := capability.NewToolRegistry()
	if err := toolprovider.RegisterNative(secondRegistry, toolprovider.NativeDependencies{Store: secondData, Conversation: secondConversation}); err != nil {
		t.Fatal(err)
	}
	secondResults := make(chan string, 1)
	secondAgent := &postgresApplicationAgent{
		registry: secondRegistry, conversation: secondConversation,
		key: idempotencyKey, content: "beta", results: secondResults,
	}
	secondService := newPostgresApplicationTestServer(secondData, secondAgent, "second transcript")
	secondHTTP := httptest.NewServer(secondService.Handler())
	defer secondHTTP.Close()

	runPostgresApplicationTurn(t, secondHTTP.URL, deviceID, credential, threadID, "turn-2")
	conflict := waitPostgresApplicationResult(t, secondResults)
	if toolResultOK(conflict) || !strings.Contains(conflict, "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("same key with different semantic request did not fail closed: %s", conflict)
	}
	notes, err = secondData.ListNotes(ctx, userID, 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "alpha" {
		t.Fatalf("idempotency conflict mutated PostgreSQL state: notes=%+v err=%v", notes, err)
	}

	if _, err := os.Stat(sqlitePath); !os.IsNotExist(err) {
		t.Fatalf("application PostgreSQL E2E unexpectedly created SQLite product DB %q: %v", sqlitePath, err)
	}
}

func newPostgresApplicationTestServer(data *Store, agent pipeline.Agent, transcript string) *server.Server {
	return server.New(pipeline.Components{
		ASR: pipeline.MockASR{Transcript: transcript},
		Agent: agent,
		TTS: pipeline.MockTTS{Frames: 1},
		Codecs: pipeline.OpusFactory{},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)),
		server.WithStore(data),
		server.WithDeviceAuthenticator(data),
		server.WithDeviceCredentialManager(data),
		server.WithEntitlementManager(data),
	)
}

func assertPostgresApplicationUnauthorized(t *testing.T, baseURL, deviceID, credential string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v2/device", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Device-Id", deviceID)
	req.Header.Set("Authorization", "Bearer "+credential)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong PostgreSQL-backed credential status=%d, want 401", resp.StatusCode)
	}
}

func runPostgresApplicationTurn(t *testing.T, baseURL, deviceID, credential, threadID, turnID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	headers := http.Header{}
	headers.Set("Device-Id", deviceID)
	headers.Set("Authorization", "Bearer "+credential)
	headers.Set("Thread-Id", threadID)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(baseURL, "http")+"/v2/device", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	audio := protocol.DefaultAudioParams()
	hello, err := protocol.Encode(protocol.SessionHelloType, protocol.Metadata{MessageID: turnID + "-hello"}, protocol.HelloPayload{Transport: protocol.Transport, AudioParams: audio})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	kind, raw, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText {
		t.Fatalf("session.ready read kind=%v err=%v", kind, err)
	}
	envelope, err := protocol.Decode(raw)
	if err != nil || envelope.Type != protocol.SessionReadyType || envelope.SessionID == "" {
		t.Fatalf("invalid session.ready: envelope=%+v err=%v", envelope, err)
	}

	start, err := protocol.Encode(protocol.TurnListenType, protocol.Metadata{MessageID: turnID + "-start", SessionID: envelope.SessionID, TurnID: turnID}, protocol.ListenPayload{State: "start", Mode: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, start); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	stop, err := protocol.Encode(protocol.TurnListenType, protocol.Metadata{MessageID: turnID + "-stop", SessionID: envelope.SessionID, TurnID: turnID}, protocol.ListenPayload{State: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, stop); err != nil {
		t.Fatal(err)
	}
}

func waitPostgresApplicationResult(t *testing.T, results <-chan string) string {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for application tool result")
		return ""
	}
}

func toolResultOK(content string) bool {
	var result struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal([]byte(content), &result) == nil && result.OK
}
