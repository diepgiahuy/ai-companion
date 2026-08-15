package ownerauth

import (
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	humanClaimMarker           = "human-code:"
	humanClaimCodeLength       = 10
	claimCodeAttemptsPerMinute = 6
	defaultHumanClaimCodeTTL   = 5 * time.Minute
)

const humanClaimCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var claimCodeLimiter = struct {
	sync.Mutex
	entries map[string]claimCodeAttemptWindow
}{entries: make(map[string]claimCodeAttemptWindow)}

type claimCodeAttemptWindow struct {
	started time.Time
	count   int
}

type claimCodeRedemption struct {
	redemptionKey    string
	rawAuthorization string
	claim            ClaimAuthorization
}

// A consumed human code remains retryable only to the same device-generated
// redemption ID. This closes the response-loss gap without making the human code
// itself replayable after a successful redemption. State is deliberately
// process-local and bounded by the same short ClaimTTL as owner claim auth.
var claimCodeRedemptions = struct {
	sync.Mutex
	entries map[*Service]map[string]claimCodeRedemption
}{entries: make(map[*Service]map[string]claimCodeRedemption)}

func normalizeHumanClaimCode(raw string) string {
	var b strings.Builder
	b.Grow(humanClaimCodeLength)
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if !strings.ContainsRune(humanClaimCodeAlphabet, r) {
			return ""
		}
		b.WriteRune(r)
	}
	if b.Len() != humanClaimCodeLength {
		return ""
	}
	return b.String()
}

func displayHumanClaimCode(normalized string) string {
	if len(normalized) != humanClaimCodeLength {
		return normalized
	}
	return normalized[:5] + "-" + normalized[5:]
}

func randomHumanClaimCode() (string, error) {
	var random [humanClaimCodeLength]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	out := make([]byte, humanClaimCodeLength)
	for i, value := range random {
		out[i] = humanClaimCodeAlphabet[int(value)&31]
	}
	return string(out), nil
}

func (s *Service) humanClaimTTL() time.Duration {
	if s.cfg.ClaimTTL > 0 {
		return s.cfg.ClaimTTL
	}
	return defaultHumanClaimCodeTTL
}

func (s *Service) MintBoundHumanClaimCode(rawSession, csrf, bootstrapID, deviceID string) (string, ClaimAuthorization, error) {
	session, err := s.AuthenticateMutation(rawSession, csrf)
	if err != nil {
		return "", ClaimAuthorization{}, err
	}
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	if bootstrapID == "" || len(bootstrapID) > 128 || deviceID == "" || len(deviceID) > 128 {
		return "", ClaimAuthorization{}, ErrInvalidClaim
	}
	binding := claimBinding(bootstrapID, deviceID)
	now := s.now()
	claim := ClaimAuthorization{
		UserID:      session.UserID,
		BootstrapID: humanClaimMarker + binding,
		ExpiresAt:   now.Add(s.humanClaimTTL()),
	}

	for attempt := 0; attempt < 4; attempt++ {
		code, codeErr := randomHumanClaimCode()
		if codeErr != nil {
			return "", ClaimAuthorization{}, codeErr
		}
		key := tokenKey(code)
		s.mu.Lock()
		s.pruneLocked(now)
		_, exists := s.claims[key]
		if !exists {
			s.claims[key] = claim
		}
		s.mu.Unlock()
		if !exists {
			return displayHumanClaimCode(code), claim, nil
		}
	}
	return "", ClaimAuthorization{}, ErrInvalidClaim
}

func redemptionReplay(s *Service, codeKey, binding, redemptionID string, now time.Time) (string, ClaimAuthorization, bool) {
	claimCodeRedemptions.Lock()
	defer claimCodeRedemptions.Unlock()
	serviceEntries := claimCodeRedemptions.entries[s]
	if serviceEntries == nil {
		return "", ClaimAuthorization{}, false
	}
	for key, entry := range serviceEntries {
		if !entry.claim.ExpiresAt.After(now) {
			delete(serviceEntries, key)
		}
	}
	if len(serviceEntries) == 0 {
		delete(claimCodeRedemptions.entries, s)
		return "", ClaimAuthorization{}, false
	}
	entry, ok := serviceEntries[codeKey]
	if !ok || entry.claim.BootstrapID != binding || entry.redemptionKey != tokenKey(redemptionID) {
		return "", ClaimAuthorization{}, false
	}
	return entry.rawAuthorization, entry.claim, true
}

func rememberRedemption(s *Service, codeKey, redemptionID, rawAuthorization string, claim ClaimAuthorization) {
	claimCodeRedemptions.Lock()
	defer claimCodeRedemptions.Unlock()
	serviceEntries := claimCodeRedemptions.entries[s]
	if serviceEntries == nil {
		serviceEntries = make(map[string]claimCodeRedemption)
		claimCodeRedemptions.entries[s] = serviceEntries
	}
	serviceEntries[codeKey] = claimCodeRedemption{
		redemptionKey:    tokenKey(redemptionID),
		rawAuthorization: rawAuthorization,
		claim:            claim,
	}
}

// RedeemBoundHumanClaimCode consumes a human code once. A retry carrying the
// same high-entropy device-generated redemption ID gets the same authorization;
// a different redemption ID cannot replay a consumed code.
func (s *Service) RedeemBoundHumanClaimCode(rawCode, bootstrapID, deviceID, redemptionID string) (string, ClaimAuthorization, error) {
	code := normalizeHumanClaimCode(rawCode)
	bootstrapID = strings.TrimSpace(bootstrapID)
	deviceID = strings.TrimSpace(deviceID)
	redemptionID = strings.TrimSpace(redemptionID)
	if code == "" || bootstrapID == "" || len(bootstrapID) > 128 || deviceID == "" || len(deviceID) > 128 ||
		len(redemptionID) < 8 || len(redemptionID) > 128 {
		return "", ClaimAuthorization{}, ErrInvalidClaim
	}
	key := tokenKey(code)
	binding := claimBinding(bootstrapID, deviceID)
	now := s.now()

	if raw, claim, ok := redemptionReplay(s, key, binding, redemptionID, now); ok {
		return raw, claim, nil
	}

	s.mu.Lock()
	claim, ok := s.claims[key]
	if ok && !claim.ExpiresAt.After(now) {
		delete(s.claims, key)
		ok = false
	}
	if !ok || claim.BootstrapID != humanClaimMarker+binding {
		s.mu.Unlock()
		return "", ClaimAuthorization{}, ErrInvalidClaim
	}
	rawAuthorization, err := randomToken(32)
	if err != nil {
		s.mu.Unlock()
		return "", ClaimAuthorization{}, err
	}
	delete(s.claims, key)
	claim.BootstrapID = binding
	s.claims[tokenKey(rawAuthorization)] = claim
	s.mu.Unlock()
	rememberRedemption(s, key, redemptionID, rawAuthorization, claim)
	return rawAuthorization, claim, nil
}

func allowClaimCodeAttempt(remote string, now time.Time) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remote))
	if err != nil || host == "" {
		host = strings.TrimSpace(remote)
	}
	if host == "" {
		host = "unknown"
	}
	claimCodeLimiter.Lock()
	defer claimCodeLimiter.Unlock()
	for key, window := range claimCodeLimiter.entries {
		if now.Sub(window.started) >= time.Minute {
			delete(claimCodeLimiter.entries, key)
		}
	}
	window, ok := claimCodeLimiter.entries[host]
	if !ok || now.Sub(window.started) >= time.Minute {
		claimCodeLimiter.entries[host] = claimCodeAttemptWindow{started: now, count: 1}
		return true
	}
	if window.count >= claimCodeAttemptsPerMinute {
		return false
	}
	window.count++
	claimCodeLimiter.entries[host] = window
	return true
}

const claimCodePageHTML = `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Claim Companion</title><style>body{font-family:sans-serif;max-width:34rem;margin:2rem auto;padding:0 1rem}label{display:block;margin:.8rem 0}input{width:100%;padding:.6rem;box-sizing:border-box}button{padding:.7rem 1rem}code{font-size:1.5rem}</style><h1>Claim Companion</h1><p>Confirm the device reference shown by your Companion setup page.</p><form id=f><label>Bootstrap reference<input id=b name=bootstrap_id required maxlength=128></label><label>Device ID<input id=d name=device_id required maxlength=128></label><button>Generate claim code</button></form><p id=o></p><script>const q=new URLSearchParams(location.search);b.value=q.get('bootstrap_id')||'';d.value=q.get('device_id')||'';function cookie(n){for(const p of document.cookie.split(';')){const [k,...v]=p.trim().split('=');if(k===n)return decodeURIComponent(v.join('='))}return''}f.onsubmit=async e=>{e.preventDefault();o.textContent='Generating...';const r=await fetch(location.pathname,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':cookie('__Host-companion_csrf')},body:JSON.stringify({bootstrap_id:b.value,device_id:d.value})});if(!r.ok){o.textContent=r.status===401?'Sign in to your Companion owner account first.':'Unable to generate code.';return}const x=await r.json();o.replaceChildren(document.createTextNode('Enter this code on the local setup page: '));const c=document.createElement('code');c.textContent=x.claim_code;o.appendChild(c)}</script>`

func (s *Service) HandleHumanClaimCode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, err := s.Authenticate(sessionCookie(r)); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `<!doctype html><title>Companion sign in</title><p>Sign in before claiming this Companion.</p><a href="/v1/owner/auth/login">Sign in</a>`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, claimCodePageHTML)
	case http.MethodPost:
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
		code, claim, err := s.MintBoundHumanClaimCode(sessionCookie(r), r.Header.Get("X-CSRF-Token"), request.BootstrapID, request.DeviceID)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"claim_code": code, "expires_at": claim.ExpiresAt})
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Service) HandleHumanClaimCodeRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !allowClaimCodeAttempt(r.RemoteAddr, s.now()) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var request struct {
		ClaimCode    string `json:"claim_code"`
		BootstrapID  string `json:"bootstrap_id"`
		DeviceID     string `json:"device_id"`
		RedemptionID string `json:"redemption_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	raw, claim, err := s.RedeemBoundHumanClaimCode(request.ClaimCode, request.BootstrapID, request.DeviceID, request.RedemptionID)
	if err != nil {
		http.Error(w, "invalid or expired claim code", http.StatusGone)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"claim_authorization": raw, "expires_at": claim.ExpiresAt})
}
