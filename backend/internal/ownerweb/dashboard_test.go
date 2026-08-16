package ownerweb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/ownerauth"
	"companion-server/internal/privacy"
	"companion-server/internal/store"
)

func newTestAuthService(t *testing.T, userID string) (*ownerauth.Service, string, string, func()) {
	t.Helper()
	var expectedChallenge string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			verifier := r.Form.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			if base64.RawURLEncoding.EncodeToString(digest[:]) != expectedChallenge {
				t.Fatal("PKCE verifier does not match challenge")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-tok", "token_type": "Bearer"})
		case "/userinfo":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"sub": userID})
		default:
			http.NotFound(w, r)
		}
	}))

	service, err := ownerauth.New(ownerauth.Config{
		AuthorizationURL: provider.URL + "/authorize",
		TokenURL:         provider.URL + "/token",
		UserInfoURL:      provider.URL + "/userinfo",
		ClientID:         "test-client",
		RedirectURL:      "https://companion.test/callback",
		Scopes:           []string{"openid", "profile"},
		LoginTTL:         time.Minute,
		SessionTTL:       time.Hour,
		HTTPClient:       provider.Client(),
	})
	if err != nil {
		provider.Close()
		t.Fatal(err)
	}

	authURL, err := service.BeginLogin()
	if err != nil {
		provider.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		provider.Close()
		t.Fatal(err)
	}
	expectedChallenge = parsed.Query().Get("code_challenge")
	state := parsed.Query().Get("state")

	rawSession, csrf, _, err := service.CompleteLogin(context.Background(), state, "auth-code")
	if err != nil {
		provider.Close()
		t.Fatal(err)
	}

	return service, rawSession, csrf, provider.Close
}

func TestOwnerWebDashboardUnauthenticated(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_unauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	handler := NewHandler(Dependencies{Store: data})
	req := httptest.NewRequest(http.MethodGet, "/v1/owner/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d want = %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "AI Companion • Workspace") {
		t.Fatalf("body missing workspace title: %s", w.Body.String())
	}
}

func TestOwnerWebDashboardAuthEnabledRedirectsUnauthenticated(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_auth_redir.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	authService, _, _, cleanup := newTestAuthService(t, "user-123")
	defer cleanup()

	handler := NewHandler(Dependencies{Store: data, Auth: authService})

	req := httptest.NewRequest(http.MethodGet, "/v1/owner/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dashboard request must return 401, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Companion Sign In") {
		t.Fatalf("expected login redirect HTML, got: %s", w.Body.String())
	}
}

func TestOwnerWebAuthAndCSRFEnforcement(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_csrf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	authService, sessionVal, csrfVal, cleanup := newTestAuthService(t, "user-alice")
	defer cleanup()

	handler := NewHandler(Dependencies{Store: data, Auth: authService})

	// 1. Read endpoint without session cookie -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/v1/owner/data/overview", nil)
	wUnauth := httptest.NewRecorder()
	handler.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth read should be 401, got %d", wUnauth.Code)
	}

	// 2. Read endpoint with valid session cookie -> 200
	reqAuthRead := httptest.NewRequest(http.MethodGet, "/v1/owner/data/overview", nil)
	reqAuthRead.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionVal})
	wAuthRead := httptest.NewRecorder()
	handler.ServeHTTP(wAuthRead, reqAuthRead)
	if wAuthRead.Code != http.StatusOK {
		t.Fatalf("auth read should be 200, got %d: %s", wAuthRead.Code, wAuthRead.Body.String())
	}

	// 3. Mutation without CSRF token -> 401
	reqNoCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/notes", strings.NewReader(`{"content":"test note"}`))
	reqNoCSRF.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionVal})
	wNoCSRF := httptest.NewRecorder()
	handler.ServeHTTP(wNoCSRF, reqNoCSRF)
	if wNoCSRF.Code != http.StatusUnauthorized {
		t.Fatalf("mutation without CSRF token must be 401, got %d", wNoCSRF.Code)
	}

	// 4. Mutation with wrong CSRF token -> 401
	reqBadCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/notes", strings.NewReader(`{"content":"test note"}`))
	reqBadCSRF.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionVal})
	reqBadCSRF.Header.Set("X-CSRF-Token", "invalid-csrf-token")
	wBadCSRF := httptest.NewRecorder()
	handler.ServeHTTP(wBadCSRF, reqBadCSRF)
	if wBadCSRF.Code != http.StatusUnauthorized {
		t.Fatalf("mutation with wrong CSRF token must be 401, got %d", wBadCSRF.Code)
	}

	// 5. Mutation with valid CSRF token -> 200 OK
	reqValidCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/notes", strings.NewReader(`{"content":"secure note"}`))
	reqValidCSRF.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionVal})
	reqValidCSRF.Header.Set("X-CSRF-Token", csrfVal)
	wValidCSRF := httptest.NewRecorder()
	handler.ServeHTTP(wValidCSRF, reqValidCSRF)
	if wValidCSRF.Code != http.StatusOK {
		t.Fatalf("mutation with valid CSRF token must be 200, got %d: %s", wValidCSRF.Code, wValidCSRF.Body.String())
	}
}

func TestOwnerWebCrossOwnerDataIsolation(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_iso.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	ctx := context.Background()
	// Seed data for Alice
	_ = data.CreateNote(ctx, "user-alice", "n_alice", "alice secret note")
	_ = data.CreateExpense(ctx, "user-alice", "e_alice", 150000, "shopping", "alice dress", time.Now().UTC())
	_ = data.CreateJournal(ctx, "user-alice", "j_alice", "alice diary entry", time.Now().UTC())
	_ = data.SetPrivacyPolicy(ctx, privacy.Policy{
		UserID:         "user-alice",
		SaveVoiceAudio: false,
		UpdatedAt:      time.Now().UTC(),
	})

	// Seed data for Bob
	_ = data.CreateNote(ctx, "user-bob", "n_bob", "bob secret note")
	_ = data.CreateExpense(ctx, "user-bob", "e_bob", 50000, "coffee", "bob latte", time.Now().UTC())
	_ = data.CreateJournal(ctx, "user-bob", "j_bob", "bob diary entry", time.Now().UTC())
	_ = data.SetPrivacyPolicy(ctx, privacy.Policy{
		UserID:         "user-bob",
		SaveVoiceAudio: true,
		UpdatedAt:      time.Now().UTC(),
	})

	authAlice, sessionAlice, csrfAlice, cleanAlice := newTestAuthService(t, "user-alice")
	defer cleanAlice()

	authBob, sessionBob, csrfBob, cleanBob := newTestAuthService(t, "user-bob")
	defer cleanBob()

	handlerAlice := NewHandler(Dependencies{Store: data, Auth: authAlice})
	handlerBob := NewHandler(Dependencies{Store: data, Auth: authBob})

	// 1. Alice query notes -> should only see Alice's note
	reqAliceNotes := httptest.NewRequest(http.MethodGet, "/v1/owner/data/notes", nil)
	reqAliceNotes.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionAlice})
	wAliceNotes := httptest.NewRecorder()
	handlerAlice.ServeHTTP(wAliceNotes, reqAliceNotes)

	if !strings.Contains(wAliceNotes.Body.String(), "alice secret note") {
		t.Fatalf("Alice should see her note: %s", wAliceNotes.Body.String())
	}
	if strings.Contains(wAliceNotes.Body.String(), "bob secret note") {
		t.Fatalf("Alice should NOT see Bob's note: %s", wAliceNotes.Body.String())
	}

	// 2. Bob query expenses -> should only see Bob's expense
	reqBobExp := httptest.NewRequest(http.MethodGet, "/v1/owner/data/expenses", nil)
	reqBobExp.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionBob})
	wBobExp := httptest.NewRecorder()
	handlerBob.ServeHTTP(wBobExp, reqBobExp)

	if !strings.Contains(wBobExp.Body.String(), "bob latte") {
		t.Fatalf("Bob should see his expense: %s", wBobExp.Body.String())
	}
	if strings.Contains(wBobExp.Body.String(), "alice dress") {
		t.Fatalf("Bob should NOT see Alice's expense: %s", wBobExp.Body.String())
	}

	// 3. Bob attempts to DELETE Alice's note (ID 1)
	reqBobDelAlice := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/notes?id=1", nil)
	reqBobDelAlice.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionBob})
	reqBobDelAlice.Header.Set("X-CSRF-Token", csrfBob)
	wBobDelAlice := httptest.NewRecorder()
	handlerBob.ServeHTTP(wBobDelAlice, reqBobDelAlice)

	// Alice verifies her note still exists
	aliceNotes, _ := data.ListNotes(ctx, "user-alice", 10)
	if len(aliceNotes) != 1 || aliceNotes[0].Content != "alice secret note" {
		t.Fatalf("Bob's delete must not affect Alice's note: %v", aliceNotes)
	}

	// 4. Privacy policy isolation
	reqAlicePriv := httptest.NewRequest(http.MethodGet, "/v1/owner/data/privacy", nil)
	reqAlicePriv.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionAlice})
	wAlicePriv := httptest.NewRecorder()
	handlerAlice.ServeHTTP(wAlicePriv, reqAlicePriv)

	var alicePolicy privacy.Policy
	if err := json.Unmarshal(wAlicePriv.Body.Bytes(), &map[string]privacy.Policy{"privacy": alicePolicy}); err == nil {
		// Verify Alice's policy has SaveVoiceAudio == false
		if strings.Contains(wAlicePriv.Body.String(), `"save_voice_audio":true`) {
			t.Fatalf("Alice should have SaveVoiceAudio false: %s", wAlicePriv.Body.String())
		}
	}
	_ = csrfAlice
}

func TestOwnerWebVoiceMemoBlobSafeDeletion(t *testing.T) {
	recordingsDir := t.TempDir()
	audioPath := filepath.Join(recordingsDir, "memo-alice-01.opus")
	if err := os.WriteFile(audioPath, []byte("audio-bytes"), 0600); err != nil {
		t.Fatal(err)
	}

	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_blobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	ctx := context.Background()
	_ = data.CreateVoiceMemo(ctx, "user-alice", "vm_alice_1", "dev-1", audioPath, "recorded memo", 4000)

	authAlice, sessionAlice, csrfAlice, cleanup := newTestAuthService(t, "user-alice")
	defer cleanup()

	handler := NewHandler(Dependencies{
		Store:         data,
		Auth:          authAlice,
		RecordingsDir: recordingsDir,
	})

	// Delete Alice's voice memo
	reqDel := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/voice-memos?id=1", nil)
	reqDel.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: sessionAlice})
	reqDel.Header.Set("X-CSRF-Token", csrfAlice)
	wDel := httptest.NewRecorder()
	handler.ServeHTTP(wDel, reqDel)

	if wDel.Code != http.StatusOK {
		t.Fatalf("delete voice memo failed: %d %s", wDel.Code, wDel.Body.String())
	}

	// Verify DB record is gone
	memos, _ := data.QueryVoiceMemos(ctx, "user-alice", store.VoiceMemoQuery{Limit: 10})
	if len(memos) != 0 {
		t.Fatalf("voice memo record should be deleted from DB, got %d", len(memos))
	}

	// Verify audio file is removed from disk
	if _, err := os.Stat(audioPath); !os.IsNotExist(err) {
		t.Fatalf("audio blob file on disk should be removed after memo deletion")
	}
}
