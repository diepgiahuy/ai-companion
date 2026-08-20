package ownerweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"companion-server/internal/store"
)

func TestOwnerPrivacyMutationValidatesAndStaysOwnerScoped(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "privacy.db"))
	if err != nil { t.Fatal(err) }
	defer data.Close()
	auth, session, csrf, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store:data, Auth:auth})

	missingCSRF := httptest.NewRequest(http.MethodPost, "/v1/owner/data/privacy", strings.NewReader(`{"save_voice_audio":true,"voice_mail_policy":"retained"}`))
	addOwnerSession(missingCSRF, session); missingW := httptest.NewRecorder(); handler.ServeHTTP(missingW, missingCSRF)
	if missingW.Code != http.StatusUnauthorized { t.Fatalf("missing CSRF status=%d", missingW.Code) }

	invalidPolicy := httptest.NewRequest(http.MethodPost, "/v1/owner/data/privacy", strings.NewReader(`{"voice_mail_policy":"anything","conversation_retention_days":30,"voice_memo_retention_days":30,"memory_retention_days":90}`))
	addOwnerSession(invalidPolicy, session); invalidPolicy.Header.Set("X-CSRF-Token", csrf); invalidW := httptest.NewRecorder(); handler.ServeHTTP(invalidW, invalidPolicy)
	if invalidW.Code != http.StatusBadRequest { t.Fatalf("invalid policy status=%d body=%s", invalidW.Code, invalidW.Body.String()) }

	invalidRetention := httptest.NewRequest(http.MethodPost, "/v1/owner/data/privacy", strings.NewReader(`{"voice_mail_policy":"disabled","conversation_retention_days":-1,"voice_memo_retention_days":30,"memory_retention_days":90}`))
	addOwnerSession(invalidRetention, session); invalidRetention.Header.Set("X-CSRF-Token", csrf); invalidRetentionW := httptest.NewRecorder(); handler.ServeHTTP(invalidRetentionW, invalidRetention)
	if invalidRetentionW.Code != http.StatusBadRequest { t.Fatalf("invalid retention status=%d body=%s", invalidRetentionW.Code, invalidRetentionW.Body.String()) }

	valid := httptest.NewRequest(http.MethodPost, "/v1/owner/data/privacy", strings.NewReader(`{"user_id":"bob","save_voice_audio":true,"voice_mail_policy":"retained","long_term_memory_enabled":true,"conversation_retention_days":45,"voice_memo_retention_days":60,"memory_retention_days":120}`))
	addOwnerSession(valid, session); valid.Header.Set("X-CSRF-Token", csrf); validW := httptest.NewRecorder(); handler.ServeHTTP(validW, valid)
	if validW.Code != http.StatusOK { t.Fatalf("valid privacy status=%d body=%s", validW.Code, validW.Body.String()) }
	alice, ok, err := data.GetPrivacyPolicy(context.Background(), "alice"); if err != nil || !ok { t.Fatalf("alice policy missing ok=%v err=%v", ok, err) }
	if !alice.SaveVoiceAudio || alice.VoiceMailPolicy != "retained" || !alice.LongTermMemoryEnabled || alice.ConversationRetentionDays != 45 || alice.VoiceMemoRetentionDays != 60 || alice.MemoryRetentionDays != 120 { t.Fatalf("unexpected alice policy: %+v", alice) }
	if _, bobExists, err := data.GetPrivacyPolicy(context.Background(), "bob"); err != nil || bobExists { t.Fatalf("request-supplied user_id escaped owner scope: bobExists=%v err=%v", bobExists, err) }
}
