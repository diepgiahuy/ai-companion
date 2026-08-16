package ownerweb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/store"
)

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

func TestOwnerWebDataEndpoints(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "ownerweb_data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	ctx := context.Background()
	_ = data.CreateNote(ctx, "default", "n1", "buy espresso beans")
	_ = data.CreateExpense(ctx, "default", "e1", 45000, "coffee", "morning latte", time.Now().UTC())

	handler := NewHandler(Dependencies{Store: data})

	// Test Overview
	reqOverview := httptest.NewRequest(http.MethodGet, "/v1/owner/data/overview", nil)
	wOverview := httptest.NewRecorder()
	handler.ServeHTTP(wOverview, reqOverview)
	if wOverview.Code != http.StatusOK {
		t.Fatalf("overview status = %d", wOverview.Code)
	}
	var overview struct {
		MonthTotal int64 `json:"month_total"`
	}
	if err := json.Unmarshal(wOverview.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.MonthTotal != 45000 {
		t.Fatalf("overview month total = %d want 45000", overview.MonthTotal)
	}

	// Test Notes Search
	reqNotes := httptest.NewRequest(http.MethodGet, "/v1/owner/data/notes?search=espresso", nil)
	wNotes := httptest.NewRecorder()
	handler.ServeHTTP(wNotes, reqNotes)
	if wNotes.Code != http.StatusOK {
		t.Fatalf("notes status = %d", wNotes.Code)
	}
	if !strings.Contains(wNotes.Body.String(), "buy espresso beans") {
		t.Fatalf("notes body missing search hit: %s", wNotes.Body.String())
	}

	// Test Device Telemetry
	reqDevice := httptest.NewRequest(http.MethodGet, "/v1/owner/data/device", nil)
	wDevice := httptest.NewRecorder()
	handler.ServeHTTP(wDevice, reqDevice)
	if wDevice.Code != http.StatusOK {
		t.Fatalf("device status = %d", wDevice.Code)
	}
	if !strings.Contains(wDevice.Body.String(), "ESP32-S3-WROOM-1-N16R8") {
		t.Fatalf("device body missing hardware info: %s", wDevice.Body.String())
	}

	// Test Create Note
	reqCreateNote := httptest.NewRequest(http.MethodPost, "/v1/owner/data/notes", strings.NewReader(`{"content":"plan weekend hike"}`))
	wCreateNote := httptest.NewRecorder()
	handler.ServeHTTP(wCreateNote, reqCreateNote)
	if wCreateNote.Code != http.StatusOK {
		t.Fatalf("create note status = %d: %s", wCreateNote.Code, wCreateNote.Body.String())
	}

	// Test Create Expense
	reqCreateExp := httptest.NewRequest(http.MethodPost, "/v1/owner/data/expenses", strings.NewReader(`{"amount_vnd":120000,"category":"food","description":"dinner"}`))
	wCreateExp := httptest.NewRecorder()
	handler.ServeHTTP(wCreateExp, reqCreateExp)
	if wCreateExp.Code != http.StatusOK {
		t.Fatalf("create expense status = %d: %s", wCreateExp.Code, wCreateExp.Body.String())
	}

	// Test Set Budget
	reqBudget := httptest.NewRequest(http.MethodPost, "/v1/owner/data/budget", strings.NewReader(`{"period":"monthly","limit_vnd":15000000}`))
	wBudget := httptest.NewRecorder()
	handler.ServeHTTP(wBudget, reqBudget)
	if wBudget.Code != http.StatusOK {
		t.Fatalf("set budget status = %d: %s", wBudget.Code, wBudget.Body.String())
	}

	// Test Devices List
	reqDevices := httptest.NewRequest(http.MethodGet, "/v1/owner/data/devices", nil)
	wDevices := httptest.NewRecorder()
	handler.ServeHTTP(wDevices, reqDevices)
	if wDevices.Code != http.StatusOK || !strings.Contains(wDevices.Body.String(), "devices") {
		t.Fatalf("devices list failed: %s", wDevices.Body.String())
	}

	// Test Device Claim
	reqClaim := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/claim", strings.NewReader(`{"claim_code":"7K4N9X"}`))
	wClaim := httptest.NewRecorder()
	handler.ServeHTTP(wClaim, reqClaim)
	if wClaim.Code != http.StatusOK || !strings.Contains(wClaim.Body.String(), "companion-s3-7K4N9X") {
		t.Fatalf("claim failed: %s", wClaim.Body.String())
	}

	// Test OTA Trigger
	reqOTA := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/ota-trigger", strings.NewReader(`{"device_id":"companion-s3-01","target_version":"v2.4.1"}`))
	wOTA := httptest.NewRecorder()
	handler.ServeHTTP(wOTA, reqOTA)
	if wOTA.Code != http.StatusOK || !strings.Contains(wOTA.Body.String(), "command_queued") {
		t.Fatalf("ota trigger failed: %s", wOTA.Body.String())
	}

	// Test Delete Expense
	reqDelExp := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/expenses?id=1", nil)
	wDelExp := httptest.NewRecorder()
	handler.ServeHTTP(wDelExp, reqDelExp)
	if wDelExp.Code != http.StatusOK || !strings.Contains(wDelExp.Body.String(), "deleted") {
		t.Fatalf("delete expense failed: %s", wDelExp.Body.String())
	}

	// Test Delete Note
	reqDelNote := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/notes?id=1", nil)
	wDelNote := httptest.NewRecorder()
	handler.ServeHTTP(wDelNote, reqDelNote)
	if wDelNote.Code != http.StatusOK || !strings.Contains(wDelNote.Body.String(), "deleted") {
		t.Fatalf("delete note failed: %s", wDelNote.Body.String())
	}

	// Test Voice Memos Query & Search
	_ = data.CreateVoiceMemo(ctx, "default", "vm1", "companion-s3-01", "/audio/vm1.opus", "buy milk and eggs", 5000)
	reqVM := httptest.NewRequest(http.MethodGet, "/v1/owner/data/voice-memos?search=milk&device_id=companion-s3-01", nil)
	wVM := httptest.NewRecorder()
	handler.ServeHTTP(wVM, reqVM)
	if wVM.Code != http.StatusOK || !strings.Contains(wVM.Body.String(), "buy milk") {
		t.Fatalf("voice memo search failed: %s", wVM.Body.String())
	}

	// Test Delete Voice Memo
	reqDelVM := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/voice-memos?id=1", nil)
	wDelVM := httptest.NewRecorder()
	handler.ServeHTTP(wDelVM, reqDelVM)
	if wDelVM.Code != http.StatusOK || !strings.Contains(wDelVM.Body.String(), "deleted") {
		t.Fatalf("delete voice memo failed: %s", wDelVM.Body.String())
	}

	// Test Create Reminder & Timer
	reqCreateRem := httptest.NewRequest(http.MethodPost, "/v1/owner/data/reminders", strings.NewReader(`{"title":"water plants","delay_seconds":300}`))
	wCreateRem := httptest.NewRecorder()
	handler.ServeHTTP(wCreateRem, reqCreateRem)
	if wCreateRem.Code != http.StatusOK || !strings.Contains(wCreateRem.Body.String(), "timer") {
		t.Fatalf("create timer failed: %s", wCreateRem.Body.String())
	}

	// Test List Reminders
	reqListRem := httptest.NewRequest(http.MethodGet, "/v1/owner/data/reminders", nil)
	wListRem := httptest.NewRecorder()
	handler.ServeHTTP(wListRem, reqListRem)
	if wListRem.Code != http.StatusOK || !strings.Contains(wListRem.Body.String(), "water plants") {
		t.Fatalf("list reminders failed: %s", wListRem.Body.String())
	}

	// Test Pause & Resume Timer
	reqPause := httptest.NewRequest(http.MethodPost, "/v1/owner/data/timers/pause", strings.NewReader(`{"id":1}`))
	wPause := httptest.NewRecorder()
	handler.ServeHTTP(wPause, reqPause)
	if wPause.Code != http.StatusOK || !strings.Contains(wPause.Body.String(), "paused") {
		t.Fatalf("pause timer failed: %s", wPause.Body.String())
	}

	reqResume := httptest.NewRequest(http.MethodPost, "/v1/owner/data/timers/resume", strings.NewReader(`{"id":1}`))
	wResume := httptest.NewRecorder()
	handler.ServeHTTP(wResume, reqResume)
	if wResume.Code != http.StatusOK || !strings.Contains(wResume.Body.String(), "resumed") {
		t.Fatalf("resume timer failed: %s", wResume.Body.String())
	}

	// Test Delete Reminder
	reqDelRem := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/reminders?id=1", nil)
	wDelRem := httptest.NewRecorder()
	handler.ServeHTTP(wDelRem, reqDelRem)
	if wDelRem.Code != http.StatusOK || !strings.Contains(wDelRem.Body.String(), "deleted") {
		t.Fatalf("delete reminder failed: %s", wDelRem.Body.String())
	}

	// Test Journal Create, List & Delete
	reqCreateJ := httptest.NewRequest(http.MethodPost, "/v1/owner/data/journal", strings.NewReader(`{"content":"shipped version 2.4 today"}`))
	wCreateJ := httptest.NewRecorder()
	handler.ServeHTTP(wCreateJ, reqCreateJ)
	if wCreateJ.Code != http.StatusOK || !strings.Contains(wCreateJ.Body.String(), "journal") {
		t.Fatalf("create journal failed: %s", wCreateJ.Body.String())
	}

	reqListJ := httptest.NewRequest(http.MethodGet, "/v1/owner/data/journal", nil)
	wListJ := httptest.NewRecorder()
	handler.ServeHTTP(wListJ, reqListJ)
	if wListJ.Code != http.StatusOK || !strings.Contains(wListJ.Body.String(), "shipped version 2.4") {
		t.Fatalf("list journal failed: %s", wListJ.Body.String())
	}

	reqDelJ := httptest.NewRequest(http.MethodDelete, "/v1/owner/data/journal?id=1", nil)
	wDelJ := httptest.NewRecorder()
	handler.ServeHTTP(wDelJ, reqDelJ)
	if wDelJ.Code != http.StatusOK || !strings.Contains(wDelJ.Body.String(), "deleted") {
		t.Fatalf("delete journal failed: %s", wDelJ.Body.String())
	}

	// Test Filter Expenses with Date Range
	reqExpRange := httptest.NewRequest(http.MethodGet, "/v1/owner/data/expenses?from=2026-08-01T00:00:00Z&to=2026-08-31T23:59:59Z&category=food", nil)
	wExpRange := httptest.NewRecorder()
	handler.ServeHTTP(wExpRange, reqExpRange)
	if wExpRange.Code != http.StatusOK || !strings.Contains(wExpRange.Body.String(), "expenses") {
		t.Fatalf("filter expenses range failed: %s", wExpRange.Body.String())
	}
}


