package ownerweb

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/domain"
)

type ownerVoiceMemo struct {
	ID                int64     `json:"id"`
	DeviceID          string    `json:"device_id,omitempty"`
	Transcript        string    `json:"transcript"`
	DurationMS        int64     `json:"duration_ms"`
	CreatedAt         time.Time `json:"created_at"`
	PlaybackAvailable bool      `json:"playback_available"`
	MediaURL          string    `json:"media_url,omitempty"`
}

func (h *Handler) projectVoiceMemo(memo domain.VoiceMemo) ownerVoiceMemo {
	_, available := h.resolveVoiceMemoPath(memo.Path)
	projected := ownerVoiceMemo{
		ID:                memo.ID,
		DeviceID:          memo.DeviceID,
		Transcript:        memo.Transcript,
		DurationMS:        memo.DurationMS,
		CreatedAt:         memo.CreatedAt,
		PlaybackAvailable: available,
	}
	if available {
		projected.MediaURL = "/v1/owner/data/voice-memos/" + strconv.FormatInt(memo.ID, 10) + "/media"
	}
	return projected
}

func (h *Handler) projectVoiceMemos(items []domain.VoiceMemo) []ownerVoiceMemo {
	out := make([]ownerVoiceMemo, 0, len(items))
	for _, item := range items {
		out = append(out, h.projectVoiceMemo(item))
	}
	return out
}

func voiceMemoMediaID(path string) (int64, bool) {
	const prefix = "data/voice-memos/"
	const suffix = "/media"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

func (h *Handler) resolveVoiceMemoPath(memoPath string) (string, bool) {
	root := strings.TrimSpace(h.deps.RecordingsDir)
	memoPath = strings.TrimSpace(memoPath)
	if root == "" || memoPath == "" {
		return "", false
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	memoAbs, err := filepath.Abs(memoPath)
	if err != nil || !pathWithin(rootAbs, memoAbs) {
		return "", false
	}

	rootResolved, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", false
	}
	memoResolved, err := filepath.EvalSymlinks(memoAbs)
	if err != nil || !pathWithin(rootResolved, memoResolved) {
		return "", false
	}
	info, err := os.Stat(memoResolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return memoResolved, true
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (h *Handler) handleVoiceMemoMedia(w http.ResponseWriter, r *http.Request, path string) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id, ok := voiceMemoMediaID(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	memo, found, err := h.deps.Store.VoiceMemoByID(r.Context(), userID, id)
	if err != nil {
		http.Error(w, "failed to load voice memo", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	resolved, ok := h.resolveVoiceMemoPath(memo.Path)
	if !ok {
		http.Error(w, "voice memo media unavailable", http.StatusNotFound)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.Error(w, "voice memo media unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "voice memo media unavailable", http.StatusNotFound)
		return
	}

	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := "voice-memo" + filepath.Ext(resolved)
	http.ServeContent(w, r, name, info.ModTime(), file)
}
