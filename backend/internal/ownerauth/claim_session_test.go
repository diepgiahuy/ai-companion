package ownerauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestOwnerAuthService(t *testing.T) *Service {
	t.Helper()
	cfg := Config{
		AuthorizationURL: "https://auth.example.com/oauth/authorize",
		TokenURL:         "https://auth.example.com/oauth/token",
		UserInfoURL:      "https://auth.example.com/userinfo",
		ClientID:         "test-client-id",
		ClientSecret:     "test-client-secret",
		RedirectURL:      "https://companion.example.com/v1/owner/auth/callback",
		Scopes:           []string{"openid", "profile"},
		LoginTTL:         5 * time.Minute,
		SessionTTL:       12 * time.Hour,
		ClaimTTL:         5 * time.Minute,
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("create test ownerauth service: %v", err)
	}
	return svc
}

func authenticateTestOwner(t *testing.T, s *Service, userID string) (rawSession string, csrfToken string) {
	t.Helper()
	rawSession, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	csrfToken, err = randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	s.mu.Lock()
	s.sessions[tokenKey(rawSession)] = sessionRecord{
		Session: Session{
			UserID:    userID,
			ExpiresAt: now.Add(s.cfg.SessionTTL),
		},
		CSRFHash: sha256.Sum256([]byte(csrfToken)),
	}
	s.mu.Unlock()
	return rawSession, csrfToken
}

func TestDeviceClaimSessionCreation(t *testing.T) {
	svc := newTestOwnerAuthService(t)

	reqBody := `{"bootstrap_id": "boot-123", "device_id": "dev-456"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	svc.HandleDeviceClaimSessions(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DeviceCode == "" || len(resp.DeviceCode) < 32 {
		t.Fatalf("invalid device_code: %s", resp.DeviceCode)
	}
	if len(resp.UserCode) != 9 || resp.UserCode[4] != '-' {
		t.Fatalf("invalid user_code format: %s", resp.UserCode)
	}
	complete, err := url.Parse(resp.VerificationURIComplete)
	if err != nil {
		t.Fatalf("parse verification_uri_complete: %v", err)
	}
	if complete.Query().Get("s") == "" || complete.Query().Get("user_code") != resp.UserCode {
		t.Fatalf("verification_uri_complete missing bound handoff values: %s", resp.VerificationURIComplete)
	}
	if resp.ExpiresIn != 300 || resp.Interval != 5 {
		t.Fatalf("unexpected timing: expires_in=%d interval=%d", resp.ExpiresIn, resp.Interval)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions", strings.NewReader(`{"bootstrap_id": ""}`))
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()
	svc.HandleDeviceClaimSessions(badW, badReq)
	if badW.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for empty bootstrap_id, got %d", badW.Code)
	}
}

func TestDeviceClaimSessionFullLifecycleAndPolling(t *testing.T) {
	svc := newTestOwnerAuthService(t)
	ownerUserID := "user-alice"
	rawSession, csrfToken := authenticateTestOwner(t, svc, ownerUserID)

	createResp, err := svc.CreateDeviceClaimSession("boot-xyz", "dev-abc", "https://companion.example.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	deviceCode := createResp["device_code"].(string)
	userCode := createResp["user_code"].(string)
	completeURI := createResp["verification_uri_complete"].(string)
	u, err := url.Parse(completeURI)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := u.Query().Get("s")
	if sessionID == "" {
		t.Fatal("empty session_id in verification URI")
	}

	pageReq := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)
	pageReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	pageW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimPage(pageW, pageReq)
	if pageW.Code != http.StatusOK {
		t.Fatalf("expected claim page 200, got %d: %s", pageW.Code, pageW.Body.String())
	}
	if !strings.Contains(pageW.Body.String(), userCode) || !strings.Contains(pageW.Body.String(), "dev-abc") {
		t.Fatalf("claim page missing user code or device id: %s", pageW.Body.String())
	}

	wrongCodeReq := httptest.NewRequest(http.MethodGet, "/v1/owner/device-claim?s="+url.QueryEscape(sessionID)+"&user_code=AAAA-AAAA", nil)
	wrongCodeReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	wrongCodeW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimPage(wrongCodeW, wrongCodeReq)
	if wrongCodeW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatched user code, got %d", wrongCodeW.Code)
	}

	pollBody := map[string]string{"device_code": deviceCode}
	pollJSON, _ := json.Marshal(pollBody)
	pollReq := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions/token", bytes.NewReader(pollJSON))
	pollW := httptest.NewRecorder()
	svc.HandleDeviceClaimSessionToken(pollW, pollReq)
	if pollW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for pending, got %d", pollW.Code)
	}
	var pollErr struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(pollW.Body.Bytes(), &pollErr)
	if pollErr.Error != "authorization_pending" {
		t.Fatalf("expected error 'authorization_pending', got %s", pollErr.Error)
	}

	pollReq2 := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions/token", bytes.NewReader(pollJSON))
	pollW2 := httptest.NewRecorder()
	svc.HandleDeviceClaimSessionToken(pollW2, pollReq2)
	if pollW2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for slow_down, got %d", pollW2.Code)
	}
	_ = json.Unmarshal(pollW2.Body.Bytes(), &pollErr)
	if pollErr.Error != "slow_down" {
		t.Fatalf("expected error 'slow_down', got %s", pollErr.Error)
	}

	unauthApproveReq := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-sessions/"+sessionID+"/approve", nil)
	unauthW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimSessionApprove(unauthW, unauthApproveReq, sessionID)
	if unauthW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated approve, got %d", unauthW.Code)
	}

	badCsrfReq := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-sessions/"+sessionID+"/approve", nil)
	badCsrfReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	badCsrfReq.Header.Set("X-CSRF-Token", "invalid-csrf-token")
	badCsrfW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimSessionApprove(badCsrfW, badCsrfReq, sessionID)
	if badCsrfW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad CSRF, got %d", badCsrfW.Code)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-sessions/"+sessionID+"/approve", nil)
	approveReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	approveReq.Header.Set("X-CSRF-Token", csrfToken)
	approveW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimSessionApprove(approveW, approveReq, sessionID)
	if approveW.Code != http.StatusOK {
		t.Fatalf("expected 200 for approve, got %d: %s", approveW.Code, approveW.Body.String())
	}
	dupApproveW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimSessionApprove(dupApproveW, approveReq, sessionID)
	if dupApproveW.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate approve, got %d", dupApproveW.Code)
	}

	originalNow := svc.now
	defer func() { svc.now = originalNow }()
	mockTime := time.Now().UTC().Add(10 * time.Second)
	svc.now = func() time.Time { return mockTime }

	pollReq3 := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions/token", bytes.NewReader(pollJSON))
	pollW3 := httptest.NewRecorder()
	svc.HandleDeviceClaimSessionToken(pollW3, pollReq3)
	if pollW3.Code != http.StatusOK {
		t.Fatalf("expected 200 after approval, got %d: %s", pollW3.Code, pollW3.Body.String())
	}
	var approvedResp struct {
		ClaimAuthorization string    `json:"claim_authorization"`
		ExpiresAt          time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(pollW3.Body.Bytes(), &approvedResp); err != nil {
		t.Fatalf("decode approved response: %v", err)
	}
	if approvedResp.ClaimAuthorization == "" {
		t.Fatal("empty claim_authorization received")
	}

	authorizedOwner, err := svc.AuthorizeDeviceClaim(approvedResp.ClaimAuthorization, "boot-xyz", "dev-abc")
	if err != nil {
		t.Fatalf("authorize device claim: %v", err)
	}
	if authorizedOwner != ownerUserID {
		t.Fatalf("expected owner %s, got %s", ownerUserID, authorizedOwner)
	}
	if _, err := svc.AuthorizeDeviceClaim(approvedResp.ClaimAuthorization, "boot-wrong", "dev-abc"); err != ErrInvalidClaim {
		t.Fatalf("expected ErrInvalidClaim on wrong bootstrap, got %v", err)
	}
	if _, err := svc.AuthorizeDeviceClaim(approvedResp.ClaimAuthorization, "boot-xyz", "dev-wrong"); err != ErrInvalidClaim {
		t.Fatalf("expected ErrInvalidClaim on wrong device, got %v", err)
	}

	mockTime = mockTime.Add(10 * time.Second)
	pollReq4 := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions/token", bytes.NewReader(pollJSON))
	pollW4 := httptest.NewRecorder()
	svc.HandleDeviceClaimSessionToken(pollW4, pollReq4)
	if pollW4.Code != http.StatusOK {
		t.Fatalf("expected 200 on replay, got %d: %s", pollW4.Code, pollW4.Body.String())
	}
	var replayedResp struct {
		ClaimAuthorization string `json:"claim_authorization"`
	}
	_ = json.Unmarshal(pollW4.Body.Bytes(), &replayedResp)
	if replayedResp.ClaimAuthorization != approvedResp.ClaimAuthorization {
		t.Fatalf("replayed token mismatch: %s != %s", replayedResp.ClaimAuthorization, approvedResp.ClaimAuthorization)
	}
}

func TestDeviceClaimSessionDenyFlow(t *testing.T) {
	svc := newTestOwnerAuthService(t)
	ownerUserID := "user-bob"
	rawSession, csrfToken := authenticateTestOwner(t, svc, ownerUserID)

	createResp, err := svc.CreateDeviceClaimSession("boot-deny", "dev-deny", "")
	if err != nil {
		t.Fatal(err)
	}
	deviceCode := createResp["device_code"].(string)
	completeURI := createResp["verification_uri_complete"].(string)
	u, _ := url.Parse(completeURI)
	sessionID := u.Query().Get("s")

	denyReq := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-sessions/"+sessionID+"/deny", nil)
	denyReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: rawSession})
	denyReq.Header.Set("X-CSRF-Token", csrfToken)
	denyW := httptest.NewRecorder()
	svc.HandleOwnerDeviceClaimSessionDeny(denyW, denyReq, sessionID)
	if denyW.Code != http.StatusOK {
		t.Fatalf("expected 200 for deny, got %d: %s", denyW.Code, denyW.Body.String())
	}

	pollBody, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	pollReq := httptest.NewRequest(http.MethodPost, "/v1/device-claim-sessions/token", bytes.NewReader(pollBody))
	pollW := httptest.NewRecorder()
	svc.HandleDeviceClaimSessionToken(pollW, pollReq)
	if pollW.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for denied session, got %d: %s", pollW.Code, pollW.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(pollW.Body.Bytes(), &resp)
	if resp.Error != "access_denied" {
		t.Fatalf("expected access_denied, got %s", resp.Error)
	}
}

func TestSafeOIDCContinuationAndOpenRedirectDefense(t *testing.T) {
	svc := newTestOwnerAuthService(t)

	validTarget, err := svc.BeginLoginWithReturnTo("/v1/owner/device-claim?s=valid-session-123")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(validTarget)
	state := parsed.Query().Get("state")
	svc.mu.Lock()
	txn, ok := svc.logins[state]
	svc.mu.Unlock()
	if !ok || txn.ReturnTo != "/v1/owner/device-claim?s=valid-session-123" {
		t.Fatalf("stored return_to mismatch: %+v", txn)
	}

	evilTarget, err := svc.BeginLoginWithReturnTo("https://evil.com/phishing")
	if err != nil {
		t.Fatal(err)
	}
	evilParsed, _ := url.Parse(evilTarget)
	evilState := evilParsed.Query().Get("state")
	svc.mu.Lock()
	evilTxn := svc.logins[evilState]
	svc.mu.Unlock()
	if evilTxn.ReturnTo != "" {
		t.Fatalf("evil return_to was not sanitized to empty: %s", evilTxn.ReturnTo)
	}

	protoRelTarget, err := svc.BeginLoginWithReturnTo("//evil.com/phishing")
	if err != nil {
		t.Fatal(err)
	}
	protoRelParsed, _ := url.Parse(protoRelTarget)
	protoRelState := protoRelParsed.Query().Get("state")
	svc.mu.Lock()
	protoRelTxn := svc.logins[protoRelState]
	svc.mu.Unlock()
	if protoRelTxn.ReturnTo != "" {
		t.Fatalf("protocol-relative return_to was not sanitized: %s", protoRelTxn.ReturnTo)
	}
}
