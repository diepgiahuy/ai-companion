package ownerweb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const ownerEditRequestLimit = 16 << 10

type ownerExpenseUpdateRequest struct {
	ID          int64  `json:"id"`
	AmountVND   int64  `json:"amount_vnd"`
	Category    string `json:"category"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
}

type ownerNoteUpdateRequest struct {
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

type ownerJournalUpdateRequest struct {
	ID         int64  `json:"id"`
	Content    string `json:"content"`
	OccurredAt string `json:"occurred_at"`
}

type ownerScheduledUpdateRequest struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	FireAt string `json:"fire_at"`
}

type ownerScheduledCancelRequest struct {
	ID int64 `json:"id"`
}

func decodeOwnerMutation(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, ownerEditRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request must contain one JSON object")
	}
	return nil
}

func requiredOwnerTime(raw, field string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must use RFC3339", field)
	}
	return parsed.UTC(), nil
}

func ownerMutationFailure(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	message := err.Error()
	if strings.Contains(message, "not found") || strings.Contains(message, "not mutable") {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	http.Error(w, "mutation failed", http.StatusInternalServerError)
}

func (h *Handler) handleUpdateExpense(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ownerExpenseUpdateRequest
	if err := decodeOwnerMutation(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "valid id is required", http.StatusBadRequest)
		return
	}
	if req.AmountVND <= 0 || req.AmountVND > 1_000_000_000 {
		http.Error(w, "amount_vnd is outside the accepted range", http.StatusBadRequest)
		return
	}
	occurredAt, err := requiredOwnerTime(req.OccurredAt, "occurred_at")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		category = "other"
	}
	if err := h.deps.Store.UpdateExpense(r.Context(), userID, req.ID, req.AmountVND, category, strings.TrimSpace(req.Description), occurredAt); err != nil {
		ownerMutationFailure(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "saved": "expense", "id": req.ID})
}

func (h *Handler) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ownerNoteUpdateRequest
	if err := decodeOwnerMutation(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(req.Content)
	if req.ID <= 0 {
		http.Error(w, "valid id is required", http.StatusBadRequest)
		return
	}
	if content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	if err := h.deps.Store.UpdateNote(r.Context(), userID, req.ID, content); err != nil {
		ownerMutationFailure(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "saved": "note", "id": req.ID})
}

func (h *Handler) handleUpdateJournal(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ownerJournalUpdateRequest
	if err := decodeOwnerMutation(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(req.Content)
	if req.ID <= 0 {
		http.Error(w, "valid id is required", http.StatusBadRequest)
		return
	}
	if content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	occurredAt, err := requiredOwnerTime(req.OccurredAt, "occurred_at")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.deps.Store.UpdateJournal(r.Context(), userID, req.ID, content, occurredAt); err != nil {
		ownerMutationFailure(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "saved": "journal", "id": req.ID})
}

func (h *Handler) handleUpdateReminder(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ownerScheduledUpdateRequest
	if err := decodeOwnerMutation(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(req.Title)
	if req.ID <= 0 {
		http.Error(w, "valid id is required", http.StatusBadRequest)
		return
	}
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	fireAt, err := requiredOwnerTime(req.FireAt, "fire_at")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.deps.Store.UpdateScheduledItem(r.Context(), userID, req.ID, title, fireAt); err != nil {
		ownerMutationFailure(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "saved": "scheduled_item", "id": req.ID})
}

func (h *Handler) handleCancelReminder(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ownerScheduledCancelRequest
	if err := decodeOwnerMutation(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID <= 0 {
		http.Error(w, "valid id is required", http.StatusBadRequest)
		return
	}
	if err := h.deps.Store.CancelScheduledItem(r.Context(), userID, req.ID); err != nil {
		ownerMutationFailure(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "cancelled": "scheduled_item", "id": req.ID})
}
