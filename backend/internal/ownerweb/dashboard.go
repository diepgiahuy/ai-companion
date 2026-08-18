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

type Handler struct {
	deps Dependencies
}

func NewHandler(deps Dependencies) *Handler {
	return &Handler{deps: deps}
}

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
		if isMutation {
			csrf := r.Header.Get("X-CSRF-Token")
			session, err = h.deps.Auth.AuthenticateMutation(cookie.Value, csrf)
		} else {
			session, err = h.deps.Auth.Authenticate(cookie.Value)
		}
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
	case path == "dashboard" && r.Method == http.MethodGet:
		h.handleDashboard(w, r)
	case path == "data/overview" && r.Method == http.MethodGet:
		h.handleOverview(w, r)
	case path == "data/expenses" && r.Method == http.MethodGet:
		h.handleExpenses(w, r)
	case path == "data/expenses" && r.Method == http.MethodPost:
		h.handleCreateExpense(w, r)
	case path == "data/expenses" && r.Method == http.MethodDelete:
		h.handleDeleteExpense(w, r)
	case path == "data/budget" && r.Method == http.MethodPost:
		h.handleSetBudget(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodGet:
		h.handleGetSavingsGoal(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodPost:
		h.handleSetSavingsGoal(w, r)
	case path == "data/savings-goal" && r.Method == http.MethodDelete:
		h.handleDeleteSavingsGoal(w, r)
	case path == "data/notes" && r.Method == http.MethodGet:
		h.handleNotes(w, r)
	case path == "data/notes" && r.Method == http.MethodPost:
		h.handleCreateNote(w, r)
	case path == "data/notes" && r.Method == http.MethodDelete:
		h.handleDeleteNote(w, r)
	case path == "data/voice-memos" && r.Method == http.MethodGet:
		h.handleVoiceMemos(w, r)
	case path == "data/voice-memos" && r.Method == http.MethodDelete:
		h.handleDeleteVoiceMemo(w, r)
	case path == "data/journal" && r.Method == http.MethodGet:
		h.handleJournal(w, r)
	case path == "data/journal" && r.Method == http.MethodPost:
		h.handleCreateJournal(w, r)
	case path == "data/journal" && r.Method == http.MethodDelete:
		h.handleDeleteJournal(w, r)
	case path == "data/reminders" && r.Method == http.MethodGet:
		h.handleReminders(w, r)
	case path == "data/reminders" && r.Method == http.MethodPost:
		h.handleCreateReminder(w, r)
	case path == "data/reminders" && r.Method == http.MethodDelete:
		h.handleDeleteReminder(w, r)
	case path == "data/timers/pause" && r.Method == http.MethodPost:
		h.handlePauseTimer(w, r)
	case path == "data/timers/resume" && r.Method == http.MethodPost:
		h.handleResumeTimer(w, r)
	case path == "data/devices" && r.Method == http.MethodGet:
		h.handleDevices(w, r)
	case path == "data/device" && r.Method == http.MethodGet:
		h.handleDevice(w, r)
	case path == "data/device/claim" && r.Method == http.MethodPost:
		h.handleClaimDevice(w, r)
	case path == "data/device/ota-trigger" && r.Method == http.MethodPost:
		h.handleTriggerOTA(w, r)
	case path == "data/device/config" && r.Method == http.MethodPost:
		h.handleUpdateDeviceConfig(w, r)
	case path == "data/privacy" && r.Method == http.MethodGet:
		h.handleGetPrivacy(w, r)
	case path == "data/privacy" && r.Method == http.MethodPost:
		h.handleSetPrivacy(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	_, _ = io.WriteString(w, dashboardHTML)
}

func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	endOfMonth := startOfMonth.AddDate(0, 1, 0)

	monthTotal, _ := h.deps.Store.ExpenseTotal(ctx, userID, startOfMonth, endOfMonth)
	monthlyBudget, budgetSet, _ := h.deps.Store.BudgetLimit(ctx, userID, "monthly")
	recentExpenses, _ := h.deps.Store.ListExpenses(ctx, userID, startOfMonth, endOfMonth, "", 5)
	recentNotes, _ := h.deps.Store.ListNotes(ctx, userID, 5)
	recentVoiceMemos, _ := h.deps.Store.ListVoiceMemos(ctx, userID, "", 5)
	activeReminders, _ := h.deps.Store.ListReminders(ctx, userID, "", "active", 5)

	goal, goalSet, _ := h.deps.Store.GetSavingsGoal(ctx, userID, "monthly")
	var gPtr *domain.SavingsGoal
	if goalSet {
		gPtr = &goal
	}
	var bPtr *int64
	if budgetSet {
		bPtr = &monthlyBudget
	}
	savingsProgress := domain.CalculateSavingsProgress(gPtr, "monthly", startOfMonth, endOfMonth, monthTotal, bPtr)

	resp := map[string]any{
		"user_id":          userID,
		"current_time":     now.Format(time.RFC3339),
		"month_total":      monthTotal,
		"monthly_budget":   monthlyBudget,
		"budget_set":       budgetSet,
		"savings_progress": savingsProgress,
		"expenses":         recentExpenses,
		"notes":            recentNotes,
		"voice_memos":      recentVoiceMemos,
		"reminders":        activeReminders,
	}
	writeJSON(w, resp)
}

func (h *Handler) handleExpenses(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	from, to := parseQueryRange(r)
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	limit := parseQueryLimit(r, 50)

	items, err := h.deps.Store.ListExpenses(ctx, userID, from, to, category, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, _ := h.deps.Store.ExpenseTotal(ctx, userID, from, to)
	writeJSON(w, map[string]any{"total_vnd": total, "from": from, "to": to, "expenses": items})
}

func (h *Handler) handleNotes(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	from, to := parseQueryRange(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit := parseQueryLimit(r, 50)

	items, err := h.deps.Store.QueryNotes(ctx, userID, domain.NoteQuery{
		From:   from,
		To:     to,
		Search: search,
		Limit:  limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"notes": items})
}

func (h *Handler) handleVoiceMemos(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	from, to := parseQueryRange(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	limit := parseQueryLimit(r, 50)

	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	items, err := h.deps.Store.QueryVoiceMemos(ctx, userID, domain.VoiceMemoQuery{
		DeviceID: deviceID,
		From:     from,
		To:       to,
		Search:   search,
		Limit:    limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"voice_memos": items})
}

func (h *Handler) handleJournal(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	from, to := parseQueryRange(r)
	limit := parseQueryLimit(r, 50)

	items, err := h.deps.Store.ListJournal(ctx, userID, from, to, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"journal": items})
}

func (h *Handler) handleReminders(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	limit := parseQueryLimit(r, 50)

	reminders, _ := h.deps.Store.ListReminders(ctx, userID, deviceID, "active", limit)
	timers, _ := h.deps.Store.ListTimers(ctx, userID, deviceID, "active", limit)
	writeJSON(w, map[string]any{"reminders": reminders, "timers": timers})
}

func (h *Handler) handleDevices(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()

	devices, err := h.deps.Store.ListUserDevices(ctx, userID)
	if err != nil || devices == nil {
		devices = []domain.DeviceItem{}
	}

	writeJSON(w, map[string]any{
		"devices": devices,
	})
}

func (h *Handler) handleDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.deps.ControlPlane == nil {
		http.Error(w, "settings control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if deviceID == "" {
		devices, err := h.deps.Store.ListUserDevices(r.Context(), userID, 1)
		if err != nil {
			http.Error(w, "failed to load devices", http.StatusInternalServerError)
			return
		}
		if len(devices) == 0 {
			h.writeJSON(w, map[string]any{
				"device_id": "", "connection_status": "offline",
				"settings_status": controlplane.SettingsStatus{State: controlplane.SettingsUnknown},
			})
			return
		}
		deviceID = devices[0].DeviceID
	}
	twin, err := h.deps.ControlPlane.Manifest(r.Context(), userID, deviceID)
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}
	online := h.deps.DeviceOnline != nil && h.deps.DeviceOnline(deviceID)
	status, err := h.deps.ControlPlane.SettingsStatus(r.Context(), userID, deviceID, online)
	if err != nil {
		http.Error(w, "failed to load settings status", http.StatusInternalServerError)
		return
	}
	interval := ""
	if twin.Desired.OTAPollIntervalSeconds != nil {
		interval = (time.Duration(*twin.Desired.OTAPollIntervalSeconds) * time.Second).String()
	}
	h.writeJSON(w, map[string]any{
		"device_id":         deviceID,
		"connection_status": map[bool]string{true: "online", false: "offline"}[online],
		"settings_status":   status,
		"desired_version":   twin.DesiredVersion,
		"reported_version":  twin.ReportedVersion,
		"ota_poll_interval": interval,
		"firmware_version":  "unknown",
		"wifi_rssi_dbm":     nil,
	})
}

func (h *Handler) handleClaimDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ClaimCode string `json:"claim_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(strings.TrimSpace(req.ClaimCode)) != 6 {
		http.Error(w, "claim_code must be 6 alphanumeric characters", http.StatusBadRequest)
		return
	}

	code := strings.ToUpper(strings.TrimSpace(req.ClaimCode))
	deviceID := fmt.Sprintf("companion-s3-%s", code)

	writeJSON(w, map[string]any{
		"ok":        true,
		"device_id": deviceID,
		"message":   "Device claimed successfully into owner account",
	})
}

func (h *Handler) handleTriggerOTA(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		DeviceID   string `json:"device_id"`
		TargetSlot string `json:"target_slot"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
		http.Error(w, "device_id is required", http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"ok":          true,
		"device_id":   req.DeviceID,
		"target_slot": "ota_1",
		"version":     "v2.4.1",
		"message":     "Firmware OTA trigger dispatched",
	})
}

func (h *Handler) handleUpdateDeviceConfig(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.deps.UpdateDeviceSettings == nil {
		http.Error(w, "settings update unavailable", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		DeviceID        string `json:"device_id"`
		OTAPollInterval string `json:"ota_poll_interval"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "request must contain one JSON object", http.StatusBadRequest)
		return
	}
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	if body.DeviceID == "" {
		http.Error(w, "device_id required", http.StatusBadRequest)
		return
	}
	interval, err := time.ParseDuration(strings.TrimSpace(body.OTAPollInterval))
	if err != nil || interval <= 0 || interval%time.Second != 0 {
		http.Error(w, "ota_poll_interval must be a whole-second duration", http.StatusBadRequest)
		return
	}
	seconds64 := int64(interval / time.Second)
	if seconds64 > int64(^uint(0)>>1) {
		http.Error(w, "ota_poll_interval is out of range", http.StatusBadRequest)
		return
	}
	seconds := int(seconds64)
	patch := controlplane.RuntimeConfig{OTAPollIntervalSeconds: &seconds}
	twin, status, err := h.deps.UpdateDeviceSettings(r.Context(), userID, body.DeviceID, patch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.writeJSON(w, map[string]any{"ok": true, "twin": twin, "settings_status": status})
}

func (h *Handler) handleGetPrivacy(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	pol, okPolicy, err := h.deps.Store.GetPrivacyPolicy(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !okPolicy {
		pol = privacy.Policy{
			UserID:                    userID,
			SaveVoiceAudio:            false,
			VoiceMailPolicy:           "disabled",
			LongTermMemoryEnabled:     false,
			ConversationRetentionDays: 30,
			VoiceMemoRetentionDays:    30,
			MemoryRetentionDays:       90,
			UpdatedAt:                 time.Now().UTC(),
		}
	}
	writeJSON(w, map[string]any{"privacy": pol})
}

func (h *Handler) handleSetPrivacy(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.userID(r)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	var req privacy.Policy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid privacy payload", http.StatusBadRequest)
		return
	}
	req.UserID = userID
	req.UpdatedAt = time.Now().UTC()
	if err := h.deps.Store.SetPrivacyPolicy(ctx, req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "privacy": req})
}

func (h *Handler) userID(r *http.Request) (string, bool) {
	if session, ok := ownerauth.CurrentSession(r.Context()); ok && session.UserID != "" {
		return session.UserID, true
	}
	if h.deps.Auth == nil {
		return "default", true
	}
	return "", false
}

func parseQueryRange(r *http.Request) (time.Time, time.Time) {
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	if fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			from = t.UTC()
		}
	}
	if toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			to = t.UTC()
		}
	}
	return from, to
}

func parseQueryLimit(r *http.Request, fallback int) int {
	limStr := strings.TrimSpace(r.URL.Query().Get("limit"))
	if limStr == "" {
		return fallback
	}
	n, err := strconv.Atoi(limStr)
	if err != nil || n < 1 {
		return fallback
	}
	if n > 100 {
		return 100
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

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
