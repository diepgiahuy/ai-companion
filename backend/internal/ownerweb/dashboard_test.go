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
	if wDevices.Code != http.StatusOK || !strings.Contains(wDevices.Body.String(), "companion-s3-01") {
		t.Fatalf("devices list failed: %s", wDevices.Body.String())
	}

	// Test Device Claim
	reqClaim := httptest.NewRequest(http.MethodPost, "/v1/owner/data/device/claim", strings.NewReader(`{"claim_code":"7K4N9X"}`))
	wClaim := httptest.NewRecorder()
	handler.ServeHTTP(wClaim, reqClaim)
	if wClaim.Code != http.StatusOK || !strings.Contains(wClaim.Body.String(), "companion-s3-7K4N9X") {
		t.Fatalf("claim failed: %s", wClaim.Body.String())
	}
}


