package ownerauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidHumanClaimCode = errors.New("invalid or expired human claim code")
	ErrHumanClaimRateLimited = errors.New("human claim code exchange rate limited")
)

const (
	humanClaimCodeSymbols = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	humanClaimCodeLength  = 10
	claimExchangeLimit    = 8
	claimExchangeWindow   = time.Minute
)

type humanClaimCodeRecord struct {
	Authorization string
	Binding       string
	ExpiresAt     time.Time
}

type exchangeWindow struct {
	Started  time.Time
	Attempts int
}

// Handoff adds the consumer-facing one-code browser handoff without changing
// the authoritative claim-authorization or device-claim protocols. A human
// code can only be exchanged for the already-existing short-lived bound claim
// authorization; it is never a device credential.
type Handoff struct {
	auth *Service
	now  func() time.Time

	mu       sync.Mutex
	codes    map[string]humanClaimCodeRecord
	attempts map[string]exchangeWindow
}

func NewHandoff(auth *Service) (*Handoff, error) {
	if auth == nil {
		return nil, fmt.Errorf("owner auth service is required")
	}
	return &Handoff{
		auth:     auth,
		now:      func() time.Time { return time.Now().UTC() },
		codes:    make(map[string]humanClaimCodeRecord),
		attempts: make(map[string]exchangeWindow),
	}, nil
}

func (h *Handoff) Mint(rawSession, csrf, bootstrapID, deviceID string) (string, time.Time, error) {
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	rawAuthorization, claim, err := h.auth.MintBoundClaimAuthorization(rawSession, csrf, bootstrapID, deviceID)
	if err != nil {
		return "", time.Time{}, err
	}
	code, err := randomHumanClaimCode()
	if err != nil {
		return "", time.Time{}, err
	}
	record := humanClaimCodeRecord{
		Authorization: rawAuthorization,
		Binding:       claimBinding(bootstrapID, deviceID),
		ExpiresAt:     claim.ExpiresAt,
	}
	now := h.now()
	h.mu.Lock()
	h.pruneLocked(now)
	h.codes[tokenKey(normalizeHumanClaimCode(code))] = record
	h.mu.Unlock()
	return formatHumanClaimCode(code), record.ExpiresAt, nil
}

func (h *Handoff) Exchange(clientKey, code, bootstrapID, deviceID string) (string, time.Time, error) {
	clientKey = strings.TrimSpace(clientKey)
	if clientKey == "" {
		clientKey = "unknown"
	}
	code = normalizeHumanClaimCode(code)
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if len(code) != humanClaimCodeLength || bootstrapID == "" || deviceID == "" {
		return "", time.Time{}, ErrInvalidHumanClaimCode
	}

	now := h.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked(now)
	if !h.allowAttemptLocked(clientKey, now) {
		return "", time.Time{}, ErrHumanClaimRateLimited
	}
	record, ok := h.codes[tokenKey(code)]
	if !ok || !record.ExpiresAt.After(now) {
		return "", time.Time{}, ErrInvalidHumanClaimCode
	}
	want := claimBinding(bootstrapID, deviceID)
	if subtle.ConstantTimeCompare([]byte(record.Binding), []byte(want)) != 1 {
		return "", time.Time{}, ErrInvalidHumanClaimCode
	}
	// Consume only after the exact bound intent matches. The returned opaque
	// authorization has its own bounded TTL and existing idempotent claim retry
	// semantics; replaying the human code itself is therefore impossible.
	delete(h.codes, tokenKey(code))
	return record.Authorization, record.ExpiresAt, nil
}

func (h *Handoff) HandleOwnerPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := h.auth.Authenticate(sessionCookie(r)); err != nil {
		http.Error(w, "owner login required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, ownerHandoffHTML)
}

func (h *Handoff) HandleMint(w http.ResponseWriter, r *http.Request) {
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
	code, expiresAt, err := h.Mint(sessionCookie(r), r.Header.Get("X-CSRF-Token"), request.BootstrapID, request.DeviceID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"claim_code": code,
		"expires_at": expiresAt,
	})
}

func (h *Handoff) HandleExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Code        string `json:"claim_code"`
		BootstrapID string `json:"bootstrap_id"`
		DeviceID    string `json:"device_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	authorization, expiresAt, err := h.Exchange(remoteClientKey(r), request.Code, request.BootstrapID, request.DeviceID)
	if err != nil {
		if errors.Is(err, ErrHumanClaimRateLimited) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "invalid or expired claim code", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"claim_authorization": authorization,
		"expires_at":          expiresAt,
	})
}

func (h *Handoff) allowAttemptLocked(clientKey string, now time.Time) bool {
	window := h.attempts[clientKey]
	if window.Started.IsZero() || now.Sub(window.Started) >= claimExchangeWindow {
		window = exchangeWindow{Started: now}
	}
	if window.Attempts >= claimExchangeLimit {
		h.attempts[clientKey] = window
		return false
	}
	window.Attempts++
	h.attempts[clientKey] = window
	return true
}

func (h *Handoff) pruneLocked(now time.Time) {
	for key, record := range h.codes {
		if !record.ExpiresAt.After(now) {
			delete(h.codes, key)
		}
	}
	for key, window := range h.attempts {
		if now.Sub(window.Started) >= claimExchangeWindow {
			delete(h.attempts, key)
		}
	}
}

func randomHumanClaimCode() (string, error) {
	raw := make([]byte, humanClaimCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for index := range raw {
		raw[index] = humanClaimCodeSymbols[int(raw[index])&(len(humanClaimCodeSymbols)-1)]
	}
	return string(raw), nil
}

func normalizeHumanClaimCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func formatHumanClaimCode(value string) string {
	value = normalizeHumanClaimCode(value)
	if len(value) != humanClaimCodeLength {
		return value
	}
	return value[:5] + "-" + value[5:]
}

func remoteClientKey(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if raw := strings.TrimSpace(r.RemoteAddr); raw != "" {
		return raw
	}
	return "unknown"
}

const ownerHandoffHTML = `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Claim Companion</title><style>body{font-family:sans-serif;max-width:34rem;margin:2rem auto;padding:0 1rem}label{display:block;margin:.8rem 0}input{width:100%;padding:.6rem;box-sizing:border-box}button{padding:.7rem 1rem}#code{font:700 2rem monospace;letter-spacing:.08em}</style><h1>Claim Companion</h1><p>Enter the bootstrap and device reference shown by your Companion. The only value you copy back to the setup page is the short claim code.</p><form id=f><label>Bootstrap reference<input name=bootstrap_id required maxlength=128 autocomplete=off></label><label>Device reference<input name=device_id required maxlength=128 autocomplete=off></label><button>Create claim code</button></form><p id=code></p><pre id=o></pre><script>const q=new URLSearchParams(location.search);for(const k of ['bootstrap_id','device_id'])if(q.get(k))f.elements[k].value=q.get(k);function cookie(n){return document.cookie.split(';').map(x=>x.trim()).find(x=>x.startsWith(n+'='))?.slice(n.length+1)||''}f.onsubmit=async e=>{e.preventDefault();code.textContent='';o.textContent='Creating...';const x=Object.fromEntries(new FormData(f));const r=await fetch('/v1/owner/claim-codes',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':decodeURIComponent(cookie('__Host-companion_csrf'))},body:JSON.stringify(x)});if(!r.ok){o.textContent='Could not create claim code. Sign in again and retry.';return}const v=await r.json();code.textContent=v.claim_code;o.textContent='Enter this code on the Companion setup page. It expires shortly.'}</script>`
