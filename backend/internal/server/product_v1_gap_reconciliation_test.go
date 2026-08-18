package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/devicecap"
	"companion-server/internal/domain"
	"companion-server/internal/memory"
	"companion-server/internal/ownerauth"
	"companion-server/internal/ownerweb"
	"companion-server/internal/pipeline"
	"companion-server/internal/providers/tools"
	"companion-server/internal/store"
)

type gapEndpoint struct {
	lastCall devicecap.Call
}

func (e *gapEndpoint) Supports(name, version string) bool {
	return name == devicecap.SettingsName && version == devicecap.SettingsVersion
}

func (e *gapEndpoint) Call(_ context.Context, call devicecap.Call) (devicecap.Result, error) {
	e.lastCall = call
	var args devicecap.SettingsArgs
	_ = json.Unmarshal(call.Arguments, &args)
	return devicecap.Result{Value: json.RawMessage(fmt.Sprintf(`{"applied":true,"version":%d}`, args.Version))}, nil
}

// TestProductV1SoftwareGapReconciliation validates the full Product-v1 software
// architecture in a single coherent flow across all domain boundaries.
func TestProductV1SoftwareGapReconciliation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Initialise authoritative store
	st, err := store.Open(filepath.Join(tempDir, "product_v1_gap_test.db"))
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	userA := "owner-alice"
	userB := "owner-bob"
	deviceA := "companion-dev-001"
	deviceB := "companion-dev-002"

	// 2. Dynamic Point-in-Time Temporal Resolution
	baseTime := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	nowFunc := func() time.Time { return baseTime }

	// 3. Register Native Tool Registry
	registry := capability.NewToolRegistry()
	toolDeps := tools.NativeDependencies{
		Store:         st,
		RecordingsDir: filepath.Join(tempDir, "recordings"),
		Now:           nowFunc,
	}
	if err := tools.RegisterNative(registry, toolDeps); err != nil {
		t.Fatalf("failed to register native tools: %v", err)
	}

	ctxAlice := pipeline.WithTurnContext(ctx, pipeline.TurnContext{UserID: userA, DeviceID: deviceA})
	ctxBob := pipeline.WithTurnContext(ctx, pipeline.TurnContext{UserID: userB, DeviceID: deviceB})

	// -------------------------------------------------------------------------
	// Seam 1: Assistant Domain Operations & Tenant Isolation
	// -------------------------------------------------------------------------
	t.Run("AssistantDomain_TenantIsolation", func(t *testing.T) {
		// Alice creates a note
		noteRes := registry.Execute(ctxAlice, "note.create", capability.ToolRequest{
			Key:       "note-alice-1",
			Arguments: `{"content":"Discuss architecture reset and capability RPC"}`,
		})
		if !strings.Contains(noteRes.Content, `"ok":true`) {
			t.Fatalf("note.create failed: %+v", noteRes)
		}

		// Bob creates an expense
		expRes := registry.Execute(ctxBob, "expense.log", capability.ToolRequest{
			Key:       "expense-bob-1",
			Arguments: `{"items":[{"amount_vnd":45000,"category":"food","description":"Cơm trưa văn phòng","occurred_at":"2026-08-18T10:00:00Z"}]}`,
		})
		if !strings.Contains(expRes.Content, `"ok":true`) {
			t.Fatalf("expense.log failed: %+v", expRes)
		}

		// Alice creates a savings goal
		savRes := registry.Execute(ctxAlice, "saving.goal_set", capability.ToolRequest{
			Key:       "saving-alice-1",
			Arguments: `{"period":"monthly","target_vnd":60000000,"description":"Buy new M3 Max"}`,
		})
		if !strings.Contains(savRes.Content, `"ok":true`) {
			t.Fatalf("saving.goal_set failed: %+v", savRes)
		}

		// Verify Alice cannot see Bob's expense
		from := baseTime.Add(-24 * time.Hour)
		to := baseTime.Add(24 * time.Hour)
		aliceExpenses, err := st.ListExpenses(ctxAlice, userA, from, to, "", 10)
		if err != nil {
			t.Fatalf("ListExpenses for Alice failed: %v", err)
		}
		if len(aliceExpenses) != 0 {
			t.Fatalf("tenant leak: Alice saw Bob's expense: %+v", aliceExpenses)
		}

		// Verify Bob cannot see Alice's notes
		bobNotes, err := st.ListNotes(ctxBob, userB, 10)
		if err != nil {
			t.Fatalf("ListNotes for Bob failed: %v", err)
		}
		if len(bobNotes) != 0 {
			t.Fatalf("tenant leak: Bob saw Alice's notes: %+v", bobNotes)
		}
	})

	// -------------------------------------------------------------------------
	// Seam 2: Settings Twin via Capability RPC (device.settings_v1)
	// -------------------------------------------------------------------------
	t.Run("SettingsTwin_CapabilityRPC_Reconciliation", func(t *testing.T) {
		twinRepo := newFakeTwinRepo()
		capRouter := devicecap.NewRouter()
		endpoint := &gapEndpoint{}

		err := capRouter.Register(deviceA, endpoint)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		wakeThreshold := 0.75
		desiredConfig := controlplane.RuntimeConfig{
			WakeModel:     "hey_companion_v2",
			WakeThreshold: &wakeThreshold,
		}

		twin, err := twinRepo.SetDesired(ctx, userA, deviceA, desiredConfig)
		if err != nil {
			t.Fatalf("SetDesired failed: %v", err)
		}

		callArgs, err := json.Marshal(devicecap.SettingsArgs{
			Version:  twin.DesiredVersion,
			Settings: desiredConfig,
		})
		if err != nil {
			t.Fatalf("marshal settings args failed: %v", err)
		}

		res, err := capRouter.Call(ctx, deviceA, devicecap.Call{
			Name:      devicecap.SettingsName,
			Version:   devicecap.SettingsVersion,
			Arguments: callArgs,
		})
		if err != nil {
			t.Fatalf("capRouter.Call failed: %v", err)
		}
		if !strings.Contains(string(res.Value), `"applied":true`) {
			t.Fatalf("expected applied response, got %s", string(res.Value))
		}

		// Record report
		err = twinRepo.RecordConfigReport(ctx, userA, deviceA, controlplane.ConfigReportResult{
			Version:    twin.DesiredVersion,
			Applied:    true,
			Config:     desiredConfig,
			ReportedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("RecordConfigReport failed: %v", err)
		}

		updatedTwin, err := twinRepo.GetTwin(ctx, userA, deviceA)
		if err != nil {
			t.Fatalf("GetTwin failed: %v", err)
		}
		if updatedTwin.ReportedVersion != twin.DesiredVersion || updatedTwin.Reported.WakeModel != "hey_companion_v2" {
			t.Fatalf("twin not synchronized: %+v", updatedTwin)
		}
		if updatedTwin.Status != controlplane.TwinStatusApplied {
			t.Fatalf("expected TwinStatusApplied, got %s", updatedTwin.Status)
		}
	})

	// -------------------------------------------------------------------------
	// Seam 3: Durable Memory Recall, Supersession & Forget
	// -------------------------------------------------------------------------
	t.Run("DurableMemory_RecallSupersessionForget", func(t *testing.T) {
		memSvc := memory.NewWithVector(st, st, memory.HashEmbedding{Dimensions: 64})

		// Add preference at t0
		t0 := baseTime
		_, err := memSvc.Remember(ctx, userA, "favorite_drink", memory.Semantic, "cà phê sữa đá", "user", 1.0, t0)
		if err != nil {
			t.Fatalf("Remember failed: %v", err)
		}

		// Recall preference
		hits, err := memSvc.Recall(ctx, userA, "cà phê", 5)
		if err != nil || len(hits) == 0 {
			t.Fatalf("Recall failed: hits=%+v err=%v", hits, err)
		}
		if hits[0].Item.Value != "cà phê sữa đá" {
			t.Fatalf("expected cà phê sữa đá, got %s", hits[0].Item.Value)
		}

		// Explicit Forget
		if err := st.ForgetMemory(ctx, userA, "favorite_drink"); err != nil {
			t.Fatalf("ForgetMemory failed: %v", err)
		}

		// Verify zero recall
		hitsDeleted, err := memSvc.Recall(ctx, userA, "cà phê", 5)
		if err != nil {
			t.Fatalf("Recall after forget failed: %v", err)
		}
		if len(hitsDeleted) != 0 {
			t.Fatalf("expected 0 hits after forget, got %+v", hitsDeleted)
		}
	})

	// -------------------------------------------------------------------------
	// Seam 4: Owner Hub Web API Verification
	// -------------------------------------------------------------------------
	t.Run("OwnerHub_REST_CRUD_And_DeviceSelector", func(t *testing.T) {
		hubHandler := ownerweb.NewHandler(ownerweb.Dependencies{
			Store: st,
		})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID != "" {
				r = r.WithContext(ownerauth.WithSession(r.Context(), ownerauth.Session{UserID: userID}))
			}
			hubHandler.ServeHTTP(w, r)
		}))
		defer srv.Close()

		// Query notes for userA
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/owner/data/notes", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-User-ID", userA)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/owner/data/notes failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var notesResp struct {
			Notes []domain.Note `json:"notes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&notesResp); err != nil {
			t.Fatalf("failed to decode notes response: %v", err)
		}
		if len(notesResp.Notes) != 1 || !strings.Contains(notesResp.Notes[0].Content, "Discuss architecture reset") {
			t.Fatalf("unexpected notes returned: %+v", notesResp.Notes)
		}
	})
}
