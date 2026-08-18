package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"companion-server/internal/ownerauth"
	"companion-server/internal/pgstore"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newZeroTypingOwnerService(t *testing.T, claimSessionStore ownerauth.ClaimSessionStore, claimCodeStore ownerauth.ClaimCodeStore) *ownerauth.Service {
	t.Helper()
	svc, err := ownerauth.New(ownerauth.Config{
		AuthorizationURL:  "https://auth.example.com/oauth/authorize",
		TokenURL:          "https://auth.example.com/oauth/token",
		UserInfoURL:       "https://auth.example.com/userinfo",
		ClientID:          "test-client",
		ClientSecret:      "test-secret",
		RedirectURL:       "https://companion.example.com/v1/owner/auth/callback",
		Scopes:            []string{"openid", "profile"},
		LoginTTL:          5 * time.Minute,
		SessionTTL:        12 * time.Hour,
		ClaimTTL:          5 * time.Minute,
		ClaimSessionStore: claimSessionStore,
		ClaimCodeStore:    claimCodeStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestFullZeroTypingOnboardingLifecycleE2E(t *testing.T) {
	dsn := os.Getenv("COMPANION_POSTGRES_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://companion:companion@127.0.0.1:55432/companion?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("skipping e2e postgres test: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping e2e postgres test (ping failed): %v", err)
	}
	defer pool.Close()

	store, err := pgstore.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i + 42)
	}

	claimSessionStore := pgstore.NewPgClaimSessionStore(store)
	claimCodeStore := pgstore.NewPgClaimCodeStore(store)
	ownerAuthA := newZeroTypingOwnerService(t, claimSessionStore, claimCodeStore)

	prefix := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	deviceID := prefix + "-device"
	bootstrapID := prefix + "-boot"
	ownerUserID := prefix + "-owner"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_claim_sessions WHERE session_id LIKE $1`, prefix+"%")
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_claim_deliveries WHERE device_id = $1`, deviceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM device_credentials WHERE device_id = $1`, deviceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM devices WHERE id = $1`, deviceID)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/device-claim-sessions", ownerAuthA.HandleDeviceClaimSessions)
	mux.HandleFunc("POST /v1/device-claim-sessions/token", ownerAuthA.HandleDeviceClaimSessionToken)
	mux.HandleFunc("GET /v1/owner/device-claim", ownerAuthA.HandleOwnerDeviceClaimPage)
	mux.HandleFunc("POST /v1/owner/device-claim-sessions/{session_id}/approve", func(w http.ResponseWriter, r *http.Request) {
		ownerAuthA.HandleOwnerDeviceClaimSessionApprove(w, r, r.PathValue("session_id"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	createBody, _ := json.Marshal(map[string]string{"device_id": deviceID, "bootstrap_id": bootstrapID})
	res, err := http.Post(server.URL+"/v1/device-claim-sessions", "application/json", bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected session create 201, got %d", res.StatusCode)
	}
	var sessionResp struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
	}
	if err := json.NewDecoder(res.Body).Decode(&sessionResp); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	verificationURL, err := url.Parse(sessionResp.VerificationURIComplete)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := verificationURL.Query().Get("s")
	if sessionID == "" || verificationURL.Query().Get("user_code") != sessionResp.UserCode {
		t.Fatalf("complete verification URI missing session/code: %s", sessionResp.VerificationURIComplete)
	}

	pollBody, _ := json.Marshal(map[string]string{"device_code": sessionResp.DeviceCode})
	pollRes, err := http.Post(server.URL+"/v1/device-claim-sessions/token", "application/json", bytes.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	if pollRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected pending 400, got %d", pollRes.StatusCode)
	}
	var pollErr struct{ Error string `json:"error"` }
	_ = json.NewDecoder(pollRes.Body).Decode(&pollErr)
	pollRes.Body.Close()
	if pollErr.Error != "authorization_pending" {
		t.Fatalf("expected authorization_pending, got %s", pollErr.Error)
	}

	rawSession := prefix + "-sess-cookie"
	csrfToken := prefix + "-csrf-token"
	ownerAuthA.InjectTestSession(rawSession, csrfToken, ownerUserID, time.Now().UTC().Add(time.Hour))
	claimPageReq, _ := http.NewRequest(http.MethodGet, server.URL+verificationURL.RequestURI(), nil)
	claimPageReq.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: rawSession})
	pageRes, err := http.DefaultClient.Do(claimPageReq)
	if err != nil {
		t.Fatal(err)
	}
	if pageRes.StatusCode != http.StatusOK {
		t.Fatalf("expected claim page 200, got %d", pageRes.StatusCode)
	}
	pageRes.Body.Close()

	approveReq, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/owner/device-claim-sessions/"+sessionID+"/approve", nil)
	approveReq.AddCookie(&http.Cookie{Name: "__Host-companion_session", Value: rawSession})
	approveReq.Header.Set("X-CSRF-Token", csrfToken)
	approveRes, err := http.DefaultClient.Do(approveReq)
	if err != nil {
		t.Fatal(err)
	}
	if approveRes.StatusCode != http.StatusOK {
		t.Fatalf("expected approve 200, got %d", approveRes.StatusCode)
	}
	approveRes.Body.Close()
	_, _ = pool.Exec(context.Background(), `UPDATE device_claim_sessions SET last_poll_at = now() - interval '10 seconds' WHERE session_id = $1`, sessionID)

	pollRes2, err := http.Post(server.URL+"/v1/device-claim-sessions/token", "application/json", bytes.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	if pollRes2.StatusCode != http.StatusOK {
		t.Fatalf("expected approved poll 200, got %d", pollRes2.StatusCode)
	}
	var approvedResp struct{ ClaimAuthorization string `json:"claim_authorization"` }
	_ = json.NewDecoder(pollRes2.Body).Decode(&approvedResp)
	pollRes2.Body.Close()
	if approvedResp.ClaimAuthorization == "" {
		t.Fatal("empty claim_authorization")
	}

	// Simulate a load balancer routing the credential exchange to another
	// backend process. Service B has no process-local state from Service A.
	ownerAuthB := newZeroTypingOwnerService(t, pgstore.NewPgClaimSessionStore(store), pgstore.NewPgClaimCodeStore(store))
	claimSvcB, err := NewClaimService(store, ownerAuthB, encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	claimServerB := httptest.NewServer(claimSvcB.Handler())
	defer claimServerB.Close()

	claimReqBody, _ := json.Marshal(map[string]string{"device_id": deviceID, "bootstrap_id": bootstrapID})
	claimReq, _ := http.NewRequest(http.MethodPost, claimServerB.URL+"/v1/owner/device-claims", bytes.NewReader(claimReqBody))
	claimReq.Header.Set("Authorization", "Bearer "+approvedResp.ClaimAuthorization)
	claimReq.Header.Set("Idempotency-Key", prefix+"-idem-key-01")
	claimReq.Header.Set("Content-Type", "application/json")
	claimRes, err := http.DefaultClient.Do(claimReq)
	if err != nil {
		t.Fatal(err)
	}
	if claimRes.StatusCode != http.StatusOK {
		t.Fatalf("cross-instance device claim expected 200, got %d", claimRes.StatusCode)
	}
	var claimOutcome struct {
		DeviceID         string `json:"device_id"`
		DeviceCredential string `json:"device_credential"`
		Replayed         bool   `json:"replayed"`
	}
	_ = json.NewDecoder(claimRes.Body).Decode(&claimOutcome)
	claimRes.Body.Close()
	if claimOutcome.DeviceID != deviceID || claimOutcome.DeviceCredential == "" || claimOutcome.Replayed {
		t.Fatalf("unexpected claim outcome: %+v", claimOutcome)
	}

	// Simulate another process restart for response-loss replay. Service C also
	// starts with empty process-local ownerauth maps and must authorize from DB.
	ownerAuthC := newZeroTypingOwnerService(t, pgstore.NewPgClaimSessionStore(store), pgstore.NewPgClaimCodeStore(store))
	claimSvcC, err := NewClaimService(store, ownerAuthC, encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	claimServerC := httptest.NewServer(claimSvcC.Handler())
	defer claimServerC.Close()
	claimReq2, _ := http.NewRequest(http.MethodPost, claimServerC.URL+"/v1/owner/device-claims", bytes.NewReader(claimReqBody))
	claimReq2.Header.Set("Authorization", "Bearer "+approvedResp.ClaimAuthorization)
	claimReq2.Header.Set("Idempotency-Key", prefix+"-idem-key-01")
	claimReq2.Header.Set("Content-Type", "application/json")
	claimRes2, err := http.DefaultClient.Do(claimReq2)
	if err != nil {
		t.Fatal(err)
	}
	if claimRes2.StatusCode != http.StatusOK {
		t.Fatalf("restart replay expected 200, got %d", claimRes2.StatusCode)
	}
	var replayOutcome struct {
		DeviceCredential string `json:"device_credential"`
		Replayed         bool   `json:"replayed"`
	}
	_ = json.NewDecoder(claimRes2.Body).Decode(&replayOutcome)
	claimRes2.Body.Close()
	if replayOutcome.DeviceCredential != claimOutcome.DeviceCredential || !replayOutcome.Replayed {
		t.Fatalf("expected exact replay after restart, got %+v", replayOutcome)
	}
}
