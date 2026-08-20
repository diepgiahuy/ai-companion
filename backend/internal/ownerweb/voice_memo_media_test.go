package ownerweb

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestOwnerVoiceMemoMediaMissingAndTraversalFailClosed(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice_memo_edge_cases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	base := t.TempDir()
	root := filepath.Join(base, "recordings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}

	// 1. Directory path instead of regular file
	subDir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := data.CreateVoiceMemo(context.Background(), "alice", "dir-memo", "device-a", subDir, "directory memo", 100); err != nil {
		t.Fatal(err)
	}

	// 2. Non-existent file
	missingFile := filepath.Join(root, "does-not-exist.opus")
	if err := data.CreateVoiceMemo(context.Background(), "alice", "missing-memo", "device-a", missingFile, "missing file memo", 100); err != nil {
		t.Fatal(err)
	}

	// 3. Traversal path
	outsideFile := filepath.Join(base, "traversal.opus")
	if err := os.WriteFile(outsideFile, []byte("outside content"), 0o600); err != nil {
		t.Fatal(err)
	}
	traversalPath := filepath.Join(root, "../traversal.opus")
	if err := data.CreateVoiceMemo(context.Background(), "alice", "traversal-memo", "device-a", traversalPath, "traversal memo", 100); err != nil {
		t.Fatal(err)
	}

	auth, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth, RecordingsDir: root})

	// 4. Verify empty path fails closed directly
	if resolved, ok := handler.resolveVoiceMemoPath(""); ok || resolved != "" {
		t.Fatalf("expected empty path to fail closed, got resolved=%q ok=%v", resolved, ok)
	}

	// Check that all fail closed on media endpoint
	for _, memoKey := range []string{"dir-memo", "missing-memo", "traversal-memo"} {
		memo, found, err := data.VoiceMemoByKey(context.Background(), "alice", memoKey)
		if err != nil || !found {
			t.Fatalf("memo lookup found=%v err=%v for key %s", found, err, memoKey)
		}
		url := "/v1/owner/data/voice-memos/" + jsonNumber(memo.ID) + "/media"
		req := httptest.NewRequest(http.MethodGet, url, nil)
		addOwnerSession(req, session)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("memo key %s expected 404 got %d body=%s", memoKey, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), base) || strings.Contains(w.Body.String(), root) {
			t.Fatalf("memo key %s leaked path in error: %s", memoKey, w.Body.String())
		}
	}

	// Invalid route formats
	for _, badURL := range []string{
		"/v1/owner/data/voice-memos/abc/media",
		"/v1/owner/data/voice-memos/0/media",
		"/v1/owner/data/voice-memos/-1/media",
		"/v1/owner/data/voice-memos/999999/media",
		"/v1/owner/data/voice-memos//media",
	} {
		req := httptest.NewRequest(http.MethodGet, badURL, nil)
		addOwnerSession(req, session)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("bad URL %s expected 404 got %d body=%s", badURL, w.Code, w.Body.String())
		}
	}

	// Verify list projection marks them unavailable
	listReq := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos", nil)
	addOwnerSession(listReq, session)
	listW := httptest.NewRecorder()
	handler.ServeHTTP(listW, listReq)
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
		if item["playback_available"] != false {
			t.Fatalf("expected playback_available=false for item: %v", item)
		}
		if _, exists := item["media_url"]; exists {
			t.Fatalf("expected no media_url for item: %v", item)
		}
	}
}

func TestOwnerVoiceMemoSearchAndFilter(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "voice_memo_search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	root := filepath.Join(t.TempDir(), "recordings")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fileA := filepath.Join(root, "memo-a.opus")
	fileB := filepath.Join(root, "memo-b.opus")
	_ = os.WriteFile(fileA, []byte("audio a"), 0o600)
	_ = os.WriteFile(fileB, []byte("audio b"), 0o600)

	_ = data.CreateVoiceMemo(context.Background(), "alice", "memo-1", "device-alpha", fileA, "meeting notes on project alpha", 5000)
	_ = data.CreateVoiceMemo(context.Background(), "alice", "memo-2", "device-beta", fileB, "buy groceries and coffee", 3000)

	auth, session, _, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth, RecordingsDir: root})

	// Search query
	req := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos?search=groceries", nil)
	addOwnerSession(req, session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", w.Code, w.Body.String())
	}
	var res struct {
		VoiceMemos []map[string]any `json:"voice_memos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.VoiceMemos) != 1 {
		t.Fatalf("expected 1 matching memo, got %d", len(res.VoiceMemos))
	}
	if res.VoiceMemos[0]["transcript"] != "buy groceries and coffee" {
		t.Fatalf("unexpected matching memo: %v", res.VoiceMemos[0])
	}
	if _, exists := res.VoiceMemos[0]["path"]; exists {
		t.Fatalf("search response leaked path: %v", res.VoiceMemos[0])
	}
	if _, exists := res.VoiceMemos[0]["user_id"]; exists {
		t.Fatalf("search response leaked user_id: %v", res.VoiceMemos[0])
	}

	// Device filter query
	devReq := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos?device_id=device-alpha", nil)
	addOwnerSession(devReq, session)
	devW := httptest.NewRecorder()
	handler.ServeHTTP(devW, devReq)
	if devW.Code != http.StatusOK {
		t.Fatalf("device filter status=%d", devW.Code)
	}
	var devRes struct {
		VoiceMemos []map[string]any `json:"voice_memos"`
	}
	if err := json.Unmarshal(devW.Body.Bytes(), &devRes); err != nil {
		t.Fatal(err)
	}
	if len(devRes.VoiceMemos) != 1 || devRes.VoiceMemos[0]["device_id"] != "device-alpha" {
		t.Fatalf("unexpected device filtered result: %v", devRes.VoiceMemos)
	}
}

func jsonNumber(v int64) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(jsonNumberRaw(v), ".0"), "."))
}

func jsonNumberRaw(v int64) string {
	return fmt.Sprintf("%d", v)
}
