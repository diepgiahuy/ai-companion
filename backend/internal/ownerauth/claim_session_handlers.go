package ownerauth

import (
	"context"
	"crypto/rand"
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

func (s *Service) claimSessionStore() ClaimSessionStore {
	if s.sessionStore != nil {
		return s.sessionStore
	}
	if s.cfg.ClaimSessionStore != nil {
		return s.cfg.ClaimSessionStore
	}
	return nil
}

// MintBoundClaimAuthorizationDirect mints a short-lived claim authorization token
// for an authenticated owner and bootstrap/device pair.
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
	expiresAt := s.now().Add(s.humanClaimTTL())
	claim := ClaimAuthorization{
		UserID:      ownerUserID,
		BootstrapID: claimBinding(bootstrapID, deviceID),
		ExpiresAt:   expiresAt,
	}

	s.mu.Lock()
	s.pruneLocked(s.now())
	s.claims[tokenKey(raw)] = claim
	s.mu.Unlock()

	return raw, expiresAt, nil
}

// CreateDeviceClaimSession handles creation of a zero-typing approval session by the device.
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
	expiresAt := now.Add(ttl)

	record := ClaimSessionRecord{
		SessionID:      sessionID,
		DeviceID:       deviceID,
		BootstrapID:    bootstrapID,
		DeviceCodeHash: HashSecret(deviceCode),
		UserCodeHash:   HashSecret(userCode),
		UserCodePlain:  userCode,
		Status:         ClaimSessionPending,
		ExpiresAt:      expiresAt,
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
	verificationURIComplete := fmt.Sprintf("%s?s=%s", verificationURI, url.QueryEscape(sessionID))

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
		http.Error(w, "failed to create claim session: "+err.Error(), http.StatusBadRequest)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_request"})
		return
	}

	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}

	codeHash := HashSecret(request.DeviceCode)
	now := s.now()
	minInterval := time.Duration(defaultPollIntervalSec) * time.Second

	outcome, err := store.PollSession(r.Context(), codeHash, minInterval, now, func(bootstrapID, deviceID, ownerUserID string) (string, time.Time, error) {
		return s.MintBoundClaimAuthorizationDirect(ownerUserID, bootstrapID, deviceID)
	})
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
			return
		}
		http.Error(w, "polling failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	switch outcome.Status {
	case PollOutcomePending:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    "authorization_pending",
			"interval": outcome.IntervalSeconds,
		})
	case PollOutcomeSlowDown:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    "slow_down",
			"interval": outcome.IntervalSeconds,
		})
	case PollOutcomeDenied:
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "access_denied",
		})
	case PollOutcomeExpired:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "expired_token",
		})
	case PollOutcomeApproved:
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"claim_authorization": outcome.ClaimAuthorization,
			"expires_at":          outcome.ExpiresAt,
		})
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "expired_token",
		})
	}
}

const claimPageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Claim Companion</title>
<style>
  :root { font-family: system-ui, -apple-system, sans-serif; color: #1e293b; background: #f8fafc; }
  body { max-width: 28rem; margin: 3rem auto; padding: 0 1.5rem; }
  .card { background: #fff; border-radius: 12px; padding: 2rem; box-shadow: 0 4px 6px -1px rgb(0 0 0 / 0.05), 0 2px 4px -2px rgb(0 0 0 / 0.05); border: 1px solid #e2e8f0; }
  h1 { font-size: 1.5rem; margin: 0 0 0.75rem; font-weight: 700; color: #0f172a; }
  p { margin: 0 0 1.5rem; line-height: 1.5; color: #475569; font-size: 0.95rem; }
  .code-box { background: #f1f5f9; border-radius: 8px; padding: 1rem; text-align: center; margin-bottom: 1.5rem; }
  .code-label { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.05em; color: #64748b; margin-bottom: 0.35rem; }
  .user-code { font-family: ui-monospace, monospace; font-size: 1.85rem; font-weight: 700; letter-spacing: 0.1em; color: #0f172a; }
  .device-info { font-size: 0.85rem; color: #64748b; margin-bottom: 1.5rem; }
  .btn-group { display: flex; gap: 0.75rem; }
  button { flex: 1; padding: 0.75rem 1rem; font-size: 0.95rem; font-weight: 600; border-radius: 8px; border: none; cursor: pointer; transition: background 0.15s ease; }
  .btn-primary { background: #0284c7; color: #fff; }
  .btn-primary:hover { background: #0369a1; }
  .btn-secondary { background: #e2e8f0; color: #334155; }
  .btn-secondary:hover { background: #cbd5e1; }
  #status-msg { margin-top: 1.25rem; font-size: 0.9rem; font-weight: 500; text-align: center; }
</style>
</head>
<body>
<main class="card">
  <h1>Claim Companion</h1>
  <p>Verify that the code below matches the code displayed on your Companion's screen.</p>
  <div class="code-box">
    <div class="code-label">Verification Code</div>
    <div class="user-code" id="user-code">{{USER_CODE}}</div>
  </div>
  <div class="device-info">Device ID: <strong id="device-id">{{DEVICE_ID}}</strong></div>
  <div class="btn-group" id="actions">
    <button type="button" class="btn-primary" id="btn-approve">Approve</button>
    <button type="button" class="btn-secondary" id="btn-deny">Deny</button>
  </div>
  <div id="status-msg" aria-live="polite"></div>
</main>
<script>
  function cookie(n){for(const p of document.cookie.split(';')){const[k,...v]=p.trim().split('=');if(k===n)return decodeURIComponent(v.join('='))}return''}
  const sessionID = '{{SESSION_ID}}';
  const statusMsg = document.getElementById('status-msg');
  const actions = document.getElementById('actions');
  document.getElementById('btn-approve').onclick = async () => {
    statusMsg.textContent = 'Approving...';
    try {
      const res = await fetch('/v1/owner/device-claim-sessions/' + encodeURIComponent(sessionID) + '/approve', {
        method: 'POST',
        headers: { 'X-CSRF-Token': cookie('__Host-companion_csrf') }
      });
      if (res.ok) {
        statusMsg.textContent = 'Companion approved! Your device will now connect automatically.';
        statusMsg.style.color = '#15803d';
        actions.style.display = 'none';
      } else {
        const data = await res.json().catch(() => ({}));
        statusMsg.textContent = data.error || 'Unable to approve Companion.';
        statusMsg.style.color = '#b91c1c';
      }
    } catch(e) {
      statusMsg.textContent = 'Network error. Please try again.';
      statusMsg.style.color = '#b91c1c';
    }
  };
  document.getElementById('btn-deny').onclick = async () => {
    statusMsg.textContent = 'Denying...';
    try {
      const res = await fetch('/v1/owner/device-claim-sessions/' + encodeURIComponent(sessionID) + '/deny', {
        method: 'POST',
        headers: { 'X-CSRF-Token': cookie('__Host-companion_csrf') }
      });
      if (res.ok) {
        statusMsg.textContent = 'Claim denied.';
        statusMsg.style.color = '#b91c1c';
        actions.style.display = 'none';
      } else {
        statusMsg.textContent = 'Unable to deny claim.';
      }
    } catch(e) {
      statusMsg.textContent = 'Network error.';
    }
  };
</script>
</body>
</html>`

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

	store := s.claimSessionStore()
	if store == nil {
		http.Error(w, "claim session store unavailable", http.StatusServiceUnavailable)
		return
	}

	if sessionID == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<!doctype html><title>Claim Companion</title><p>Missing session reference. Please scan the QR code on your Companion.</p>`)
		return
	}

	record, err := store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `<!doctype html><title>Claim Companion</title><p>Session not found or expired.</p>`)
		return
	}

	if record.Status != ClaimSessionPending || !record.ExpiresAt.After(s.now()) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		_, _ = io.WriteString(w, fmt.Sprintf(`<!doctype html><title>Claim Companion</title><p>Session is %s.</p>`, html.EscapeString(string(record.Status))))
		return
	}

	page := claimPageTemplate
	page = strings.ReplaceAll(page, "{{USER_CODE}}", html.EscapeString(record.UserCodePlain))
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

	sessionID = strings.TrimSpace(sessionID)
	if err := store.ApproveSession(r.Context(), sessionID, session.UserID, s.now()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case errors.Is(err, ErrSessionNotFound):
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session not found"})
		case errors.Is(err, ErrSessionExpired):
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session expired"})
		case errors.Is(err, ErrSessionAlreadyApproved):
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session already approved"})
		case errors.Is(err, ErrSessionDenied):
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session denied"})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		}
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "approved",
		"session_id": sessionID,
	})
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

	sessionID = strings.TrimSpace(sessionID)
	if err := store.DenySession(r.Context(), sessionID, s.now()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case errors.Is(err, ErrSessionNotFound):
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session not found"})
		case errors.Is(err, ErrSessionExpired):
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "session expired"})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		}
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "denied",
		"session_id": sessionID,
	})
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

	sessionID = strings.TrimSpace(sessionID)
	rec, err := store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session_id": rec.SessionID,
		"device_id":  rec.DeviceID,
		"user_code":  rec.UserCodePlain,
		"status":     rec.Status,
		"expires_at": rec.ExpiresAt,
	})
}
