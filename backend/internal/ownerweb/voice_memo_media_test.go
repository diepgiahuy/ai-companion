package ownerweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"companion-server/internal/store"
)

func TestOwnerVoiceMemoProjectionHidesStoragePathAndStreamsOwnedMedia(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice_memo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	root := filepath.Join(t.TempDir(), "recordings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(root, "memo.opus")
	media := []byte("0123456789")
	if err := os.WriteFile(mediaPath, media, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateVoiceMemo(context.Background(), "alice", "memo-a", "device-a", mediaPath, "shopping list", 1250); err != nil {
		t.Fatal(err)
	}
	memo, found, err := data.VoiceMemoByKey(context.Background(), "alice", "memo-a")
	if err != nil || !found {
		t.Fatalf("memo lookup found=%v err=%v", found, err)
	}

	auth, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth, RecordingsDir: root})

	listReq := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos", nil)
	addOwnerSession(listReq, session)
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listW.Code, listW.Body.String())
	}
	if strings.Contains(listW.Body.String(), mediaPath) || strings.Contains(listW.Body.String(), `"path"`) {
		t.Fatalf("voice memo list leaked storage path: %s", listW.Body.String())
	}
	var list struct {
		VoiceMemos []map[string]any `json:"voice_memos"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.VoiceMemos) != 1 {
		t.Fatalf("voice memos=%v", list.VoiceMemos)
	}
	projected := list.VoiceMemos[0]
	if _, exists := projected["user_id"]; exists {
		t.Fatalf("voice memo projection leaked user_id: %v", projected)
	}
	if projected["playback_available"] != true {
		t.Fatalf("playback_available=%v", projected["playback_available"])
	}
	wantURL := "/v1/owner/data/voice-memos/" + jsonNumber(memo.ID) + "/media"
	if projected["media_url"] != wantURL {
		t.Fatalf("media_url=%v want=%s", projected["media_url"], wantURL)
	}

	overviewReq := httptest.NewRequest(http.MethodGet, "/v1/owner/data/overview", nil)
	addOwnerSession(overviewReq, session)
	overviewW := httptest.NewRecorder()
	handler.ServeHTTP(overviewW, overviewReq)
	if overviewW.Code != http.StatusOK {
		t.Fatalf("overview status=%d body=%s", overviewW.Code, overviewW.Body.String())
	}
	var overview struct {
		VoiceMemos []map[string]any `json:"voice_memos"`
	}
	if err := json.Unmarshal(overviewW.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.VoiceMemos) != 1 {
		t.Fatalf("overview voice memos=%v", overview.VoiceMemos)
	}
	if _, exists := overview.VoiceMemos[0]["path"]; exists {
		t.Fatalf("overview leaked path: %v", overview.VoiceMemos[0])
	}
	if _, exists := overview.VoiceMemos[0]["user_id"]; exists {
		t.Fatalf("overview voice memo leaked user_id: %v", overview.VoiceMemos[0])
	}

	mediaReq := httptest.NewRequest(http.MethodGet, wantURL, nil)
	addOwnerSession(mediaReq, session)
	mediaW := httptest.NewRecorder()
	handler.ServeHTTP(mediaW, mediaReq)
	if mediaW.Code != http.StatusOK || mediaW.Body.String() != string(media) {
		t.Fatalf("media status=%d body=%q", mediaW.Code, mediaW.Body.String())
	}
	if got := mediaW.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, wantURL, nil)
	addOwnerSession(rangeReq, session)
	rangeReq.Header.Set("Range", "bytes=2-5")
	rangeW := httptest.NewRecorder()
	handler.ServeHTTP(rangeW, rangeReq)
	if rangeW.Code != http.StatusPartialContent || rangeW.Body.String() != "2345" {
		t.Fatalf("range status=%d body=%q", rangeW.Code, rangeW.Body.String())
	}
}

func TestOwnerVoiceMemoMediaFailsClosedForWrongOwnerAndUnsafePaths(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice_memo_security.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	base := t.TempDir()
	root := filepath.Join(base, "recordings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside.opus")
	outside := filepath.Join(base, "private-secret.opus")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateVoiceMemo(context.Background(), "alice", "inside", "device-a", inside, "inside", 100); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateVoiceMemo(context.Background(), "alice", "outside", "device-a", outside, "outside", 100); err != nil {
		t.Fatal(err)
	}
	insideMemo, _, _ := data.VoiceMemoByKey(context.Background(), "alice", "inside")
	outsideMemo, _, _ := data.VoiceMemoByKey(context.Background(), "alice", "outside")

	aliceAuth, aliceSession, _, aliceCleanup := newTestAuthService(t, "alice")
	defer aliceCleanup()
	aliceHandler := NewHandler(Dependencies{Store: data, Auth: aliceAuth, RecordingsDir: root})
	bobAuth, bobSession, _, bobCleanup := newTestAuthService(t, "bob")
	defer bobCleanup()
	bobHandler := NewHandler(Dependencies{Store: data, Auth: bobAuth, RecordingsDir: root})

	insideURL := "/v1/owner/data/voice-memos/" + jsonNumber(insideMemo.ID) + "/media"
	bobReq := httptest.NewRequest(http.MethodGet, insideURL, nil)
	addOwnerSession(bobReq, bobSession)
	bobW := httptest.NewRecorder()
	bobHandler.ServeHTTP(bobW, bobReq)
	if bobW.Code != http.StatusNotFound {
		t.Fatalf("wrong owner status=%d body=%s", bobW.Code, bobW.Body.String())
	}

	unauthReq := httptest.NewRequest(http.MethodGet, insideURL, nil)
	unauthW := httptest.NewRecorder()
	aliceHandler.ServeHTTP(unauthW, unauthReq)
	if unauthW.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", unauthW.Code)
	}

	outsideURL := "/v1/owner/data/voice-memos/" + jsonNumber(outsideMemo.ID) + "/media"
	outsideReq := httptest.NewRequest(http.MethodGet, outsideURL, nil)
	addOwnerSession(outsideReq, aliceSession)
	outsideW := httptest.NewRecorder()
	aliceHandler.ServeHTTP(outsideW, outsideReq)
	if outsideW.Code != http.StatusNotFound {
		t.Fatalf("outside status=%d body=%s", outsideW.Code, outsideW.Body.String())
	}
	if strings.Contains(outsideW.Body.String(), outside) || strings.Contains(outsideW.Body.String(), base) {
		t.Fatalf("outside path leaked in error: %s", outsideW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos", nil)
	addOwnerSession(listReq, aliceSession)
	listW := httptest.NewRecorder()
	aliceHandler.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status=%d", listW.Code)
	}
	var list struct {
		VoiceMemos []map[string]any `json:"voice_memos"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list.VoiceMemos {
		if id, _ := item["id"].(float64); int64(id) == outsideMemo.ID {
			if item["playback_available"] != false {
				t.Fatalf("outside memo advertised playback: %v", item)
			}
			if _, exists := item["media_url"]; exists {
				t.Fatalf("outside memo advertised media URL: %v", item)
			}
		}
	}

	noRootHandler := NewHandler(Dependencies{Store: data, Auth: aliceAuth})
	noRootReq := httptest.NewRequest(http.MethodGet, insideURL, nil)
	addOwnerSession(noRootReq, aliceSession)
	noRootW := httptest.NewRecorder()
	noRootHandler.ServeHTTP(noRootW, noRootReq)
	if noRootW.Code != http.StatusNotFound {
		t.Fatalf("unconfigured recordings root status=%d", noRootW.Code)
	}
}

func TestOwnerVoiceMemoMediaRejectsSymlinkEscapeAndDeleteStaysOwnerScoped(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice_memo_symlink.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	base := t.TempDir()
	root := filepath.Join(base, "recordings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.opus")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.opus")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := data.CreateVoiceMemo(context.Background(), "alice", "link", "device-a", link, "link", 100); err != nil {
		t.Fatal(err)
	}
	memo, _, _ := data.VoiceMemoByKey(context.Background(), "alice", "link")

	aliceAuth, aliceSession, aliceCSRF, aliceCleanup := newTestAuthService(t, "alice")
	defer aliceCleanup()
	aliceHandler := NewHandler(Dependencies{Store: data, Auth: aliceAuth, RecordingsDir: root})
	bobAuth, bobSession, bobCSRF, bobCleanup := newTestAuthService(t, "bob")
	defer bobCleanup()
	bobHandler := NewHandler(Dependencies{Store: data, Auth: bobAuth, RecordingsDir: root})
	url := "/v1/owner/data/voice-memos/" + jsonNumber(memo.ID) + "/media"

	mediaReq := httptest.NewRequest(http.MethodGet, url, nil)
	addOwnerSession(mediaReq, aliceSession)
	mediaW := httptest.NewRecorder()
	aliceHandler.ServeHTTP(mediaW, mediaReq)
	if mediaW.Code != http.StatusNotFound {
		t.Fatalf("symlink escape status=%d body=%s", mediaW.Code, mediaW.Body.String())
	}

	deleteURL := "/v1/owner/data/voice-memos?id=" + jsonNumber(memo.ID)
	bobDelete := httptest.NewRequest(http.MethodDelete, deleteURL, nil)
	addOwnerSession(bobDelete, bobSession)
	bobDelete.Header.Set("X-CSRF-Token", bobCSRF)
	bobDeleteW := httptest.NewRecorder()
	bobHandler.ServeHTTP(bobDeleteW, bobDelete)
	if bobDeleteW.Code != http.StatusNotFound {
		t.Fatalf("wrong-owner delete status=%d body=%s", bobDeleteW.Code, bobDeleteW.Body.String())
	}
	if _, found, err := data.VoiceMemoByID(context.Background(), "alice", memo.ID); err != nil || !found {
		t.Fatalf("wrong-owner delete changed memo found=%v err=%v", found, err)
	}

	aliceDelete := httptest.NewRequest(http.MethodDelete, deleteURL, nil)
	addOwnerSession(aliceDelete, aliceSession)
	aliceDelete.Header.Set("X-CSRF-Token", aliceCSRF)
	aliceDeleteW := httptest.NewRecorder()
	aliceHandler.ServeHTTP(aliceDeleteW, aliceDelete)
	if aliceDeleteW.Code != http.StatusOK {
		t.Fatalf("owner delete status=%d body=%s", aliceDeleteW.Code, aliceDeleteW.Body.String())
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("deleting symlink memo must not delete outside media: %v", err)
	}
}

func jsonNumber(v int64) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(jsonNumberRaw(v), ".0"), "."))
}

func jsonNumberRaw(v int64) string {
	return fmt.Sprintf("%d", v)
}
