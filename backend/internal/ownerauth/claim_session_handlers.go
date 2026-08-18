package ownerauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	deviceClaimSessionAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	defaultSessionTTL          = 5 * time.Minute
	defaultPollIntervalSec     = 5
)

func randomUserCode() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	out := make([]byte, 8)
	for i, val := range random {
		out[i] = deviceClaimSessionAlphabet[int(val)&31]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

func validDeviceClaimUserCode(raw string) bool {
	if len(raw) != 9 || raw[4] != '-' {
		return false
	}
	for i, c := range raw {
		if i == 4 {
			continue
		}
		if !strings.ContainsRune(deviceClaimSessionAlphabet, c) {
			return false
		}
	}
	return true
}

func (s *Service) claimSessionStore() ClaimSessionStore {
	if s.sessionStore != nil {
		return s.sessionStore
	}
	if s.cfg.ClaimSessionStore != nil {
		return s.cfg.ClaimSessionStore
	}
	return nil
}

// MintBoundClaimAuthorizationDirect creates the opaque authorization returned to
// the device after owner approval. The claim-session store owns persistence and
// validation of this authorization. Do not make process-local memory its source
// of truth.
func (s *Service) MintBoundClaimAuthorizationDirect(ownerUserID, bootstrapID, deviceID string) (string, time.Time, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if ownerUserID == "" || bootstrapID == "" || len(bootstrapID) > 128 || deviceID == "" || len(deviceID) > 128 {
		return "", time.Time{}, fmt.Errorf("invalid claim authorization parameters")
	}
	raw, err := randomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	return raw, s.now().Add(s.humanClaimTTL()), nil
}

// CreateDeviceClaimSession creates one short-lived device authorization session.
// PostgreSQL stores only hashes of device_code and user_code. The complete
// verification URI carries user_code to the owner browser so it can display and
// verify the same code without a plaintext database copy.
func (s *Service) CreateDeviceClaimSession(bootstrapID, deviceID, origin string) (map[string]any, error) {
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if bootstrapID == "" || len(bootstrapID) > 128 || deviceID == "" || len(deviceID) > 128 {
		return nil, fmt.Errorf("bootstrap_id and device_id are required and must be <=128 bytes")
	}

	sessionID, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	deviceCode, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	userCode, err := randomUserCode()
	if err != nil {
		return nil, err
	}

	ttl := s.humanClaimTTL()
	now := s.now()
	record := ClaimSessionRecord{
		SessionID:      sessionID,
		DeviceID:       deviceID,
		BootstrapID:    bootstrapID,
		DeviceCodeHash: HashSecret(deviceCode),
		UserCodeHash:   HashSecret(userCode),
		Status:         ClaimSessionPending,
		ExpiresAt:      now.Add(ttl),
		CreatedAt:      now,
	}
	store := s.claimSessionStore()
	if store == nil {
		return nil, fmt.Errorf("claim session store unavailable")
	}
	if err := store.CreateSession(context.Background(), record); err != nil {
		return nil, err
	}

	verificationURI := "/v1/owner/device-claim"
	if s.cfg.PublicBaseURL != "" {
		verificationURI = strings.TrimRight(s.cfg.PublicBaseURL, "/") + "/v1/owner/device-claim"
	} else if origin != "" {
		verificationURI = strings.TrimRight(origin, "/") + "/v1/owner/device-claim"
	}
	values := url.Values{}
	values.Set("s", sessionID)
	values.Set("user_code", userCode)
	verificationURIComplete := verificationURI + "?" + values.Encode()

	return map[string]any{
		"device_code":               deviceCode,
		"user_code":                 userCode,
		"verification_uri":          verificationURI,
		"verification_uri_complete": verificationURIComplete,
		"expires_in":                int(ttl.Seconds()),
		"interval":                  defaultPollIntervalSec,
	}, nil
}

func (s *Service) HandleDeviceClaimSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		BootstrapID string `json:"bootstrap_id"`
		DeviceID    string `json:"device_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	origin := ""
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		origin = "https://" + r.Host
	} else if r.Host != "" {
		origin = "http://" + r.Host
	}
	resp, err := s.CreateDeviceClaimSession(request.BootstrapID, request.DeviceID, origin)
	if err != nil {
		http.Error(w, "failed to create claim session", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) HandleDeviceClaimSessionToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		DeviceCode string `json:"device_code"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	request.DeviceCode = strings.TrimSpace(request.DeviceCode)
	if request.DeviceCode == "" {
		writeClaimSessionError(w, http.StatusBadRequest, "invalid_request", 0)
		return
	}
	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}
	outcome, err := store.PollSession(
		r.Context(),
		HashSecret(request.DeviceCode),
		time.Duration(defaultPollIntervalSec)*time.Second,
		s.now(),
		s.MintBoundClaimAuthorizationDirect,
	)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			writeClaimSessionError(w, http.StatusBadRequest, "invalid_grant", 0)
			return
		}
		http.Error(w, "polling failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	switch outcome.Status {
	case PollOutcomePending:
		writeClaimSessionError(w, http.StatusBadRequest, "authorization_pending", outcome.IntervalSeconds)
	case PollOutcomeSlowDown:
		writeClaimSessionError(w, http.StatusBadRequest, "slow_down", outcome.IntervalSeconds)
	case PollOutcomeDenied:
		writeClaimSessionError(w, http.StatusForbidden, "access_denied", 0)
	case PollOutcomeExpired:
		writeClaimSessionError(w, http.StatusBadRequest, "expired_token", 0)
	case PollOutcomeApproved:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"claim_authorization": outcome.ClaimAuthorization,
			"expires_at":          outcome.ExpiresAt,
		})
	default:
		writeClaimSessionError(w, http.StatusBadRequest, "expired_token", 0)
	}
}

func writeClaimSessionError(w http.ResponseWriter, status int, code string, interval int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]any{"error": code}
	if interval > 0 {
		payload["interval"] = interval
	}
	_ = json.NewEncoder(w).Encode(payload)
}

const claimPageTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Claim Companion</title><style>
body{font-family:system-ui,-apple-system,sans-serif;max-width:28rem;margin:3rem auto;padding:0 1.5rem;color:#1e293b;background:#f8fafc}
.card{background:#fff;border:1px solid #e2e8f0;border-radius:12px;padding:2rem}.code{font:700 1.85rem ui-monospace,monospace;letter-spacing:.1em;padding:1rem;text-align:center;background:#f1f5f9;border-radius:8px;margin:1rem 0}.actions{display:flex;gap:.75rem}button{flex:1;padding:.75rem;border:0;border-radius:8px;font-weight:600}.approve{background:#0284c7;color:#fff}.deny{background:#e2e8f0;color:#334155}#status{margin-top:1rem;text-align:center}</style></head>
<body><main class="card"><h1>Add this Companion?</h1><p>Confirm that this code matches the code on the Companion display.</p><div class="code">{{USER_CODE}}</div><p>Device: <strong>{{DEVICE_ID}}</strong></p><div class="actions" id="actions"><button class="approve" id="approve">Approve</button><button class="deny" id="deny">Deny</button></div><div id="status" aria-live="polite"></div></main>
<script>
function cookie(n){for(const p of document.cookie.split(';')){const[k,...v]=p.trim().split('=');if(k===n)return decodeURIComponent(v.join('='))}return''}
const sid='{{SESSION_ID}}',status=document.getElementById('status'),actions=document.getElementById('actions');
async function act(name){status.textContent=name==='approve'?'Approving...':'Denying...';try{const r=await fetch('/v1/owner/device-claim-sessions/'+encodeURIComponent(sid)+'/'+name,{method:'POST',headers:{'X-CSRF-Token':cookie('__Host-companion_csrf')}});if(r.ok){status.textContent=name==='approve'?'Companion approved. It will connect automatically.':'Claim denied.';actions.style.display='none'}else{const x=await r.json().catch(()=>({}));status.textContent=x.error||'Request failed.'}}catch(_){status.textContent='Network error. Please try again.'}}
document.getElementById('approve').onclick=()=>act('approve');document.getElementById('deny').onclick=()=>act('deny');
</script></body></html>`

func (s *Service) HandleOwnerDeviceClaimPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.Authenticate(sessionCookie(r)); err != nil {
		loginURL := "/v1/owner/auth/login?return_to=" + url.QueryEscape(r.URL.RequestURI())
		http.Redirect(w, r, loginURL, http.StatusFound)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("s"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(r.URL.Query().Get("session_id"))
	}
	userCode := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("user_code")))
	if sessionID == "" || !validDeviceClaimUserCode(userCode) {
		http.Error(w, "invalid verification reference", http.StatusBadRequest)
		return
	}
	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}
	record, err := store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "verification session unavailable", http.StatusNotFound)
		return
	}
	providedHash := HashSecret(userCode)
	if subtle.ConstantTimeCompare([]byte(providedHash), []byte(record.UserCodeHash)) != 1 {
		http.Error(w, "invalid verification reference", http.StatusNotFound)
		return
	}
	if record.Status != ClaimSessionPending || !record.ExpiresAt.After(s.now()) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, fmt.Sprintf(`<!doctype html><title>Claim Companion</title><p>Session is %s.</p>`, html.EscapeString(string(record.Status))))
		return
	}
	page := strings.ReplaceAll(claimPageTemplate, "{{USER_CODE}}", html.EscapeString(userCode))
	page = strings.ReplaceAll(page, "{{DEVICE_ID}}", html.EscapeString(record.DeviceID))
	page = strings.ReplaceAll(page, "{{SESSION_ID}}", html.EscapeString(record.SessionID))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, page)
}

func (s *Service) HandleOwnerDeviceClaimSessionApprove(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := s.AuthenticateMutation(sessionCookie(r), r.Header.Get("X-CSRF-Token"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.ApproveSession(r.Context(), strings.TrimSpace(sessionID), session.UserID, s.now()); err != nil {
		writeClaimMutationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "approved", "session_id": strings.TrimSpace(sessionID)})
}

func (s *Service) HandleOwnerDeviceClaimSessionDeny(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.AuthenticateMutation(sessionCookie(r), r.Header.Get("X-CSRF-Token")); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := store.DenySession(r.Context(), strings.TrimSpace(sessionID), s.now()); err != nil {
		writeClaimMutationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "denied", "session_id": strings.TrimSpace(sessionID)})
}

func writeClaimMutationError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusBadRequest
	message := "claim session not mutable"
	switch {
	case errors.Is(err, ErrSessionNotFound):
		status, message = http.StatusNotFound, "session not found"
	case errors.Is(err, ErrSessionExpired):
		status, message = http.StatusGone, "session expired"
	case errors.Is(err, ErrSessionAlreadyApproved):
		status, message = http.StatusConflict, "session already approved"
	case errors.Is(err, ErrSessionDenied):
		status, message = http.StatusForbidden, "session denied"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

func (s *Service) HandleOwnerDeviceClaimSessionGet(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.Authenticate(sessionCookie(r)); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}
	rec, err := store.GetSessionByID(r.Context(), strings.TrimSpace(sessionID))
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": rec.SessionID,
		"device_id":  rec.DeviceID,
		"status":     rec.Status,
		"expires_at": rec.ExpiresAt,
	})
}
