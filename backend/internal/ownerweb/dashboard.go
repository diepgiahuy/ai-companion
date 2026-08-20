package ownerweb

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
	"companion-server/internal/ownerauth"
	"companion-server/internal/privacy"
)

//go:embed dashboard.html
var dashboardHTML string

type DeviceSettingsUpdater func(context.Context, string, string, controlplane.RuntimeConfig) (controlplane.Twin, controlplane.SettingsStatus, error)

type Dependencies struct {
	Store                domain.ReadRepositories
	ControlPlane         *controlplane.Service
	Auth                 *ownerauth.Service
	RecordingsDir        string
	DeviceOnline         func(string) bool
	UpdateDeviceSettings DeviceSettingsUpdater
}

type Handler struct { deps Dependencies }

func NewHandler(deps Dependencies) *Handler { return &Handler{deps: deps} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.deps.Auth != nil {
		cookie, err := r.Cookie("__Host-companion_session")
		if err != nil || cookie.Value == "" {
			if r.URL.Path == "/v1/owner/dashboard" && r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, loginRedirectHTML)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		isMutation := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		var session ownerauth.Session
		if isMutation { session, err = h.deps.Auth.AuthenticateMutation(cookie.Value, r.Header.Get("X-CSRF-Token")) } else { session, err = h.deps.Auth.Authenticate(cookie.Value) }
		if err != nil {
			if r.URL.Path == "/v1/owner/dashboard" && r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, loginRedirectHTML)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(ownerauth.WithSession(r.Context(), session))
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/owner/")
	switch {
	case path == "dashboard" && r.Method == http.MethodGet: h.handleDashboard(w, r)
	case path == "data/overview" && r.Method == http.MethodGet: h.handleOverview(w, r)
	case path == "data/expenses" && r.Method == http.MethodGet: h.handleExpenses(w, r)
	case path == "data/expenses" && r.Method == http.MethodPost: h.handleCreateExpense(w, r)
	case path == "data/expenses" && r.Method == http.MethodDelete: h.handleDeleteExpense(w, r)
	case path == "data/budget" && r.Method == http.MethodPost: h.handleSetBudget(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodGet: h.handleGetSavingsGoal(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodPost: h.handleSetSavingsGoal(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodDelete: h.handleDeleteSavingsGoal(w, r)
	case path == "data/notes" && r.Method == http.MethodGet: h.handleNotes(w, r)
	case path == "data/notes" && r.Method == http.MethodPost: h.handleCreateNote(w, r)
	case path == "data/notes" && r.Method == http.MethodDelete: h.handleDeleteNote(w, r)
	case path == "data/voice-memos" && r.Method == http.MethodGet: h.handleVoiceMemos(w, r)
	case path == "data/voice-memos" && r.Method == http.MethodDelete: h.handleDeleteVoiceMemo(w, r)
	case path == "data/journal" && r.Method == http.MethodGet: h.handleJournal(w, r)
	case path == "data/journal" && r.Method == http.MethodPost: h.handleCreateJournal(w, r)
	case path == "data/journal" && r.Method == http.MethodDelete: h.handleDeleteJournal(w, r)
	case path == "data/reminders" && r.Method == http.MethodGet: h.handleReminders(w, r)
	case path == "data/reminders" && r.Method == http.MethodPost: h.handleCreateReminder(w, r)
	case path == "data/reminders" && r.Method == http.MethodDelete: h.handleDeleteReminder(w, r)
	case path == "data/timers/pause" && r.Method == http.MethodPost: h.handlePauseTimer(w, r)
	case path == "data/timers/resume" && r.Method == http.MethodPost: h.handleResumeTimer(w, r)
	case path == "data/devices" && r.Method == http.MethodGet: h.handleDevices(w, r)
	case path == "data/device" && r.Method == http.MethodGet: h.handleDevice(w, r)
	case path == "data/device/ota-trigger" && r.Method == http.MethodPost: h.handleTriggerOTA(w, r)
	case path == "data/device/config" && r.Method == http.MethodPost: h.handleUpdateDeviceConfig(w, r)
	case path == "data/privacy" && r.Method == http.MethodGet: h.handleGetPrivacy(w, r)
	case path == "data/privacy" && r.Method == http.MethodPost: h.handleSetPrivacy(w, r)
	default: http.NotFound(w, r)
	}
}

func (h *Handler) handleDashboard(w http.ResponseWriter, _ *http.Request) { w.Header().Set("Content-Type", "text/html; charset=utf-8"); w.Header().Set("Cache-Control", "no-store, max-age=0"); _, _ = io.WriteString(w, dashboardHTML) }

func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }
	ctx := r.Context(); now := time.Now().UTC(); startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC); endOfMonth := startOfMonth.AddDate(0, 1, 0)
	monthTotal, _ := h.deps.Store.ExpenseTotal(ctx, userID, startOfMonth, endOfMonth); monthlyBudget, budgetSet, _ := h.deps.Store.BudgetLimit(ctx, userID, "monthly"); recentExpenses, _ := h.deps.Store.ListExpenses(ctx, userID, startOfMonth, endOfMonth, "", 5); recentNotes, _ := h.deps.Store.ListNotes(ctx, userID, 5); recentVoiceMemos, _ := h.deps.Store.ListVoiceMemos(ctx, userID, "", 5); activeReminders, _ := h.deps.Store.ListReminders(ctx, userID, "", "active", 5)
	goal, goalSet, _ := h.deps.Store.GetSavingsGoal(ctx, userID, "monthly"); var gPtr *domain.SavingsGoal; if goalSet { gPtr = &goal }; var bPtr *int64; if budgetSet { bPtr = &monthlyBudget }; savingsProgress := domain.CalculateSavingsProgress(gPtr, "monthly", startOfMonth, endOfMonth, monthTotal, bPtr)
	writeJSON(w, map[string]any{"user_id": userID, "current_time": now.Format(time.RFC3339), "month_total": monthTotal, "monthly_budget": monthlyBudget, "budget_set": budgetSet, "savings_progress": savingsProgress, "expenses": recentExpenses, "notes": recentNotes, "voice_memos": recentVoiceMemos, "reminders": activeReminders})
}

func (h *Handler) handleExpenses(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; from, to := parseQueryRange(r); category := strings.TrimSpace(r.URL.Query().Get("category")); limit := parseQueryLimit(r, 50); items, err := h.deps.Store.ListExpenses(r.Context(), userID, from, to, category, limit); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; total, _ := h.deps.Store.ExpenseTotal(r.Context(), userID, from, to); writeJSON(w, map[string]any{"total_vnd": total, "from": from, "to": to, "expenses": items}) }
func (h *Handler) handleNotes(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; from, to := parseQueryRange(r); items, err := h.deps.Store.QueryNotes(r.Context(), userID, domain.NoteQuery{From: from, To: to, Search: strings.TrimSpace(r.URL.Query().Get("search")), Limit: parseQueryLimit(r, 50)}); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"notes": items}) }
func (h *Handler) handleVoiceMemos(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; from, to := parseQueryRange(r); items, err := h.deps.Store.QueryVoiceMemos(r.Context(), userID, domain.VoiceMemoQuery{DeviceID: strings.TrimSpace(r.URL.Query().Get("device_id")), From: from, To: to, Search: strings.TrimSpace(r.URL.Query().Get("search")), Limit: parseQueryLimit(r, 50)}); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"voice_memos": items}) }
func (h *Handler) handleJournal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; from, to := parseQueryRange(r); items, err := h.deps.Store.ListJournal(r.Context(), userID, from, to, parseQueryLimit(r, 50)); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"journal": items}) }
func (h *Handler) handleReminders(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; deviceID := strings.TrimSpace(r.URL.Query().Get("device_id")); limit := parseQueryLimit(r, 50); reminders, _ := h.deps.Store.ListReminders(r.Context(), userID, deviceID, "active", limit); timers, _ := h.deps.Store.ListTimers(r.Context(), userID, deviceID, "active", limit); writeJSON(w, map[string]any{"reminders": reminders, "timers": timers}) }
func (h *Handler) handleDevices(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; devices, err := h.deps.Store.ListUserDevices(r.Context(), userID); if err != nil || devices == nil { devices = []domain.DeviceItem{} }; writeJSON(w, map[string]any{"devices": devices}) }

func (h *Handler) handleDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; if h.deps.ControlPlane == nil { http.Error(w, "settings control plane unavailable", http.StatusServiceUnavailable); return }
	devices, err := h.deps.Store.ListUserDevices(r.Context(), userID); if err != nil { http.Error(w, "failed to load devices", http.StatusInternalServerError); return }
	requestedID := strings.TrimSpace(r.URL.Query().Get("device_id")); deviceID := ""; if requestedID == "" && len(devices) > 0 { deviceID = devices[0].DeviceID } else { for _, device := range devices { if device.DeviceID == requestedID { deviceID = requestedID; break } } }
	if deviceID == "" { if requestedID != "" { http.Error(w, "device not found", http.StatusNotFound); return }; writeJSON(w, emptyOwnerDeviceProjection()); return }
	twin, err := h.deps.ControlPlane.Manifest(r.Context(), userID, deviceID); if err != nil { http.Error(w, "failed to load device settings", http.StatusInternalServerError); return }; online := h.deps.DeviceOnline != nil && h.deps.DeviceOnline(deviceID); status, err := h.deps.ControlPlane.SettingsStatus(r.Context(), userID, deviceID, online); if err != nil { http.Error(w, "failed to load settings status", http.StatusInternalServerError); return }; writeJSON(w, projectOwnerDevice(twin, status, online))
}

func (h *Handler) handleTriggerOTA(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; http.Error(w, "OTA trigger is not wired into Owner Hub", http.StatusNotImplemented) }

func (h *Handler) handleUpdateDeviceConfig(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; if h.deps.UpdateDeviceSettings == nil { http.Error(w, "settings update unavailable", http.StatusServiceUnavailable); return }
	var body ownerDeviceConfigRequest; decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)); decoder.DisallowUnknownFields(); if err := decoder.Decode(&body); err != nil { http.Error(w, "invalid request", http.StatusBadRequest); return }; var extra any; if err := decoder.Decode(&extra); err != io.EOF { http.Error(w, "request must contain one JSON object", http.StatusBadRequest); return }
	body.DeviceID = strings.TrimSpace(body.DeviceID); if body.DeviceID == "" { http.Error(w, "device_id required", http.StatusBadRequest); return }; devices, err := h.deps.Store.ListUserDevices(r.Context(), userID); if err != nil { http.Error(w, "failed to verify device ownership", http.StatusInternalServerError); return }; owned := false; for _, device := range devices { if device.DeviceID == body.DeviceID { owned = true; break } }; if !owned { http.Error(w, "device not found", http.StatusNotFound); return }
	patch, err := body.patch(); if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; twin, status, err := h.deps.UpdateDeviceSettings(r.Context(), userID, body.DeviceID, patch); if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; writeJSON(w, map[string]any{"ok": true, "device": projectOwnerDevice(twin, status, status.Online), "settings_status": status})
}

func (h *Handler) handleCreateExpense(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Amount int64 `json:"amount_vnd"`; Category string `json:"category"`; Description string `json:"description"`; OccurredAt string `json:"occurred_at"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 { http.Error(w, "invalid expense payload", http.StatusBadRequest); return }; occurred := time.Now().UTC(); if req.OccurredAt != "" { if t, err := time.Parse(time.RFC3339, req.OccurredAt); err == nil { occurred = t.UTC() } }; key := fmt.Sprintf("web-exp-%d", time.Now().UnixNano()); if err := h.deps.Store.CreateExpense(r.Context(), userID, key, req.Amount, req.Category, req.Description, occurred); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "saved": "expense"}) }
func (h *Handler) handleSetBudget(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Period string `json:"period"`; LimitVND int64 `json:"limit_vnd"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Period == "" || req.LimitVND < 0 { http.Error(w, "invalid budget payload", http.StatusBadRequest); return }; if err := h.deps.Store.SetBudget(r.Context(), userID, req.Period, req.LimitVND); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "saved": "budget", "period": req.Period, "limit_vnd": req.LimitVND}) }
func (h *Handler) handleDeleteExpense(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); if err != nil || id <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.DeleteExpense(r.Context(), userID, id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "deleted": "expense", "id": id}) }

func ownerMutationRequest(r *http.Request, userID, operation, clientKey string, payload any) (idempotency.Request, error) { key := strings.TrimSpace(clientKey); if key == "" { key = strings.TrimSpace(r.Header.Get("X-Idempotency-Key")) }; if key == "" { key = fmt.Sprintf("ownerweb-%s-%d", operation, time.Now().UnixNano()) }; b, err := json.Marshal(payload); if err != nil { return idempotency.Request{}, err }; hash := sha256.Sum256(append([]byte(operation+":"), b...)); return idempotency.Request{Actor: userID, Operation: operation, Key: key, RequestHash: hex.EncodeToString(hash[:])}, nil }
func (h *Handler) handleGetSavingsGoal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))); if period == "" { period = "monthly" }; now := time.Now().UTC(); start, end := domain.CalculatePeriodBounds(period, now, time.UTC); spent, _ := h.deps.Store.ExpenseTotal(r.Context(), userID, start, end); bLimit, bSet, _ := h.deps.Store.BudgetLimit(r.Context(), userID, period); var bPtr *int64; if bSet { bPtr = &bLimit }; goal, gSet, err := h.deps.Store.GetSavingsGoal(r.Context(), userID, period); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; var gPtr *domain.SavingsGoal; if gSet { gPtr = &goal }; progress := domain.CalculateSavingsProgress(gPtr, period, start, end, spent, bPtr); writeJSON(w, map[string]any{"period": period, "set": gSet, "goal": gPtr, "progress": progress}) }
func (h *Handler) handleSetSavingsGoal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Period string `json:"period"`; TargetVND int64 `json:"target_vnd"`; Description string `json:"description"`; IdempotencyKey string `json:"idempotency_key"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "invalid request body", http.StatusBadRequest); return }; if err := domain.ValidateSavingsTarget(req.TargetVND); err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; period := strings.ToLower(strings.TrimSpace(req.Period)); if period == "" { period = "monthly" }; mutator, ok := h.deps.Store.(interface { SetSavingsGoalMutation(context.Context, idempotency.Request, string, string, int64, string, time.Time) error }); if !ok { http.Error(w, "durable store required", http.StatusInternalServerError); return }; mutationReq, err := ownerMutationRequest(r, userID, "saving.goal_set", req.IdempotencyKey, map[string]any{"period": period, "target_vnd": req.TargetVND, "description": req.Description}); if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; if err := mutator.SetSavingsGoalMutation(r.Context(), mutationReq, userID, period, req.TargetVND, strings.TrimSpace(req.Description), time.Now().UTC()); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "period": period, "target_vnd": req.TargetVND}) }
func (h *Handler) handleDeleteSavingsGoal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; period := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("period"))); if period == "" { period = "monthly" }; key := strings.TrimSpace(r.URL.Query().Get("idempotency_key")); mutator, ok := h.deps.Store.(interface { DeleteSavingsGoalMutation(context.Context, idempotency.Request, string, string) error }); if !ok { http.Error(w, "durable store required", http.StatusInternalServerError); return }; mutationReq, err := ownerMutationRequest(r, userID, "saving.goal_delete", key, map[string]any{"period": period}); if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; if err := mutator.DeleteSavingsGoalMutation(r.Context(), mutationReq, userID, period); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "deleted": "savings_goal", "period": period}) }
func (h *Handler) handleCreateNote(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Content string `json:"content"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" { http.Error(w, "content is required", http.StatusBadRequest); return }; key := fmt.Sprintf("web-note-%d", time.Now().UnixNano()); if err := h.deps.Store.CreateNote(r.Context(), userID, key, strings.TrimSpace(req.Content)); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "saved": "note"}) }
func (h *Handler) handleDeleteNote(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); if err != nil || id <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.DeleteNote(r.Context(), userID, id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "deleted": "note", "id": id}) }
func (h *Handler) handleDeleteVoiceMemo(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); if err != nil || id <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; memos, _ := h.deps.Store.QueryVoiceMemos(r.Context(), userID, domain.VoiceMemoQuery{Limit: 100}); var memoPath string; for _, m := range memos { if m.ID == id { memoPath = m.Path; break } }; if err := h.deps.Store.DeleteVoiceMemo(r.Context(), userID, id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; if memoPath != "" && h.deps.RecordingsDir != "" { cleanRecordings, cleanPath := filepath.Clean(h.deps.RecordingsDir), filepath.Clean(memoPath); if strings.HasPrefix(cleanPath, cleanRecordings+string(filepath.Separator)) || cleanPath == cleanRecordings { _ = os.Remove(cleanPath) } }; writeJSON(w, map[string]any{"ok": true, "deleted": "voice_memo", "id": id}) }
func (h *Handler) handleCreateJournal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Content string `json:"content"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" { http.Error(w, "content is required", http.StatusBadRequest); return }; key := fmt.Sprintf("web-journal-%d", time.Now().UnixNano()); if err := h.deps.Store.CreateJournal(r.Context(), userID, key, strings.TrimSpace(req.Content), time.Now().UTC()); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "saved": "journal"}) }
func (h *Handler) handleDeleteJournal(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); if err != nil || id <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.DeleteJournal(r.Context(), userID, id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "deleted": "journal", "id": id}) }
func (h *Handler) handleCreateReminder(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { Kind string `json:"kind"`; Title string `json:"title"`; FireAt string `json:"fire_at"`; DelayMinutes int `json:"delay_minutes"`; DelaySeconds int `json:"delay_seconds"`; IdempotencyKey string `json:"idempotency_key"`; DeviceID string `json:"device_id"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" { http.Error(w, "title is required", http.StatusBadRequest); return }; kind := strings.TrimSpace(req.Kind); fireAt := time.Now().UTC(); if req.DelaySeconds > 0 { fireAt = fireAt.Add(time.Duration(req.DelaySeconds)*time.Second) } else if req.DelayMinutes > 0 { fireAt = fireAt.Add(time.Duration(req.DelayMinutes)*time.Minute) } else if req.FireAt != "" { if t, err := time.Parse(time.RFC3339, req.FireAt); err == nil { fireAt = t.UTC() } } else { fireAt = fireAt.Add(10*time.Minute) }; key := req.IdempotencyKey; if key == "" { key = fmt.Sprintf("web-rem-%d", time.Now().UnixNano()) }; if kind == "timer" || req.DelaySeconds > 0 || req.DelayMinutes > 0 { if err := h.deps.Store.CreateTimerForDevice(r.Context(), userID, key, req.DeviceID, strings.TrimSpace(req.Title), fireAt); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "kind": "timer", "fire_at": fireAt.Format(time.RFC3339)}); return }; if err := h.deps.Store.CreateReminderForDevice(r.Context(), userID, key, req.DeviceID, strings.TrimSpace(req.Title), fireAt); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "kind": "reminder", "fire_at": fireAt.Format(time.RFC3339)}) }
func (h *Handler) handleDeleteReminder(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); if err != nil || id <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.DeleteScheduledItem(r.Context(), userID, id); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "deleted": "reminder", "id": id}) }
func (h *Handler) handlePauseTimer(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { ID int64 `json:"id"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.PauseTimer(r.Context(), userID, req.ID, time.Now().UTC()); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "paused": true, "id": req.ID}) }
func (h *Handler) handleResumeTimer(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req struct { ID int64 `json:"id"` }; if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 { http.Error(w, "valid id is required", http.StatusBadRequest); return }; if err := h.deps.Store.ResumeTimer(r.Context(), userID, req.ID, time.Now().UTC()); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "resumed": true, "id": req.ID}) }
func (h *Handler) handleGetPrivacy(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; pol, okPolicy, err := h.deps.Store.GetPrivacyPolicy(r.Context(), userID); if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; if !okPolicy { pol = privacy.Policy{UserID: userID, SaveVoiceAudio: false, VoiceMailPolicy: "disabled", LongTermMemoryEnabled: false, ConversationRetentionDays: 30, VoiceMemoRetentionDays: 30, MemoryRetentionDays: 90, UpdatedAt: time.Now().UTC()} }; writeJSON(w, map[string]any{"privacy": pol}) }
func (h *Handler) handleSetPrivacy(w http.ResponseWriter, r *http.Request) { userID, ok := h.userID(r); if !ok || userID == "" { http.Error(w, "unauthorized", http.StatusUnauthorized); return }; var req privacy.Policy; decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)); decoder.DisallowUnknownFields(); if err := decoder.Decode(&req); err != nil { http.Error(w, "invalid privacy payload", http.StatusBadRequest); return }; req.UserID = userID; normalized, err := privacy.NormalizePolicy(req); if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }; normalized.UpdatedAt = time.Now().UTC(); if err := h.deps.Store.SetPrivacyPolicy(r.Context(), normalized); err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }; writeJSON(w, map[string]any{"ok": true, "privacy": normalized}) }
func (h *Handler) userID(r *http.Request) (string, bool) { if session, ok := ownerauth.CurrentSession(r.Context()); ok && session.UserID != "" { return session.UserID, true }; if h.deps.Auth == nil { return "default", true }; return "", false }
func parseQueryRange(r *http.Request) (time.Time, time.Time) { fromStr, toStr := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to")); now := time.Now().UTC(); from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC); to := from.AddDate(0, 1, 0); if fromStr != "" { if t, err := time.Parse(time.RFC3339, fromStr); err == nil { from = t.UTC() } }; if toStr != "" { if t, err := time.Parse(time.RFC3339, toStr); err == nil { to = t.UTC() } }; return from, to }
func parseQueryLimit(r *http.Request, fallback int) int { limStr := strings.TrimSpace(r.URL.Query().Get("limit")); if limStr == "" { return fallback }; n, err := strconv.Atoi(limStr); if err != nil || n < 1 { return fallback }; if n > 100 { return 100 }; return n }
func writeJSON(w http.ResponseWriter, v any) { w.Header().Set("Content-Type", "application/json; charset=utf-8"); w.Header().Set("Cache-Control", "no-store"); _ = json.NewEncoder(w).Encode(v) }

const loginRedirectHTML = `<!doctype html>
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Companion Sign In</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#191919;color:#e5e5e5;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.box{background:#202020;border:1px solid #2f2f2f;border-radius:8px;padding:2.5rem;text-align:center;max-width:24rem;box-shadow:0 4px 20px rgba(0,0,0,0.4)}
h1{font-size:1.4rem;margin:0 0 0.8rem 0;color:#fff}
p{color:#888;font-size:0.9rem;margin-bottom:1.5rem}
a{display:inline-block;background:#38bdf8;color:#000;padding:0.7rem 1.4rem;border-radius:6px;text-decoration:none;font-weight:600;font-size:0.95rem;transition:opacity 0.2s}
a:hover{opacity:0.9}
</style>
<div class="box">
<h1>AI Companion</h1>
<p>Sign in to your owner account to access your personal data dashboard.</p>
<a href="/v1/owner/auth/login">Sign in with Owner Account</a>
</div>`