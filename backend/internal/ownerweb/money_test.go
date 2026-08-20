package ownerweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"companion-server/internal/store"
)

func TestOwnerMonthlyBudgetMutationRequiresCSRFAndRereadsFromOverview(t *testing.T) {
	data, err := store.Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	auth, session, csrf, cleanup := newTestAuthService(t, "alice")
	defer cleanup()
	handler := NewHandler(Dependencies{Store: data, Auth: auth})

	withoutCSRF := httptest.NewRequest(
		http.MethodPost,
		"/v1/owner/data/budget",
		strings.NewReader(`{"period":"monthly","limit_vnd":15000000}`),
	)
	addOwnerSession(withoutCSRF, session)
	withoutCSRFW := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFW, withoutCSRF)
	if withoutCSRFW.Code != http.StatusUnauthorized {
		t.Fatalf("missing CSRF status=%d", withoutCSRFW.Code)
	}

	setBudget := func(limit int64) {
		t.Helper()
		req := httptest.NewRequest(
			http.MethodPost,
			"/v1/owner/data/budget",
			strings.NewReader(`{"period":"monthly","limit_vnd":`+strconv.FormatInt(limit, 10)+`}`),
		)
		addOwnerSession(req, session)
		req.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("set budget status=%d body=%s", w.Code, w.Body.String())
		}
	}

	readOverview := func() struct {
		MonthlyBudget int64 `json:"monthly_budget"`
		BudgetSet     bool  `json:"budget_set"`
	} {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/owner/data/overview", nil)
		addOwnerSession(req, session)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("overview status=%d body=%s", w.Code, w.Body.String())
		}
		var got struct {
			MonthlyBudget int64 `json:"monthly_budget"`
			BudgetSet     bool  `json:"budget_set"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	setBudget(15_000_000)
	first := readOverview()
	if !first.BudgetSet || first.MonthlyBudget != 15_000_000 {
		t.Fatalf("first overview budget=%+v", first)
	}

	setBudget(18_000_000)
	updated := readOverview()
	if !updated.BudgetSet || updated.MonthlyBudget != 18_000_000 {
		t.Fatalf("updated overview budget=%+v", updated)
	}
}
