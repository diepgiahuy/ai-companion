package ownerauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maximumProviderResponseBytes = 64 << 10
	sessionCookieName            = "__Host-companion_session"
	csrfCookieName               = "__Host-companion_csrf"
)

var (
	ErrUnauthorized = errors.New("owner session unauthorized")
	ErrInvalidState = errors.New("invalid or expired oauth state")
	ErrInvalidClaim = errors.New("invalid or expired claim authorization")
)

type Config struct {
	AuthorizationURL string
	TokenURL         string
	UserInfoURL      string
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	Scopes           []string
	LoginTTL         time.Duration
	SessionTTL       time.Duration
	ClaimTTL         time.Duration
	ClaimCodeStore   ClaimCodeStore
	HTTPClient       *http.Client
}

type Session struct {
	UserID    string
	ExpiresAt time.Time
}

type ClaimAuthorization struct {
	UserID      string
	BootstrapID string
	ExpiresAt   time.Time
}

type loginTransaction struct {
	Verifier  string
	ExpiresAt time.Time
}

type sessionRecord struct {
	Session
	CSRFHash [32]byte
}

type Service struct {
	cfg      Config
	client   *http.Client
	now      func() time.Time
	mu       sync.Mutex
	logins   map[string]loginTransaction
	sessions map[string]sessionRecord
	claims   map[string]ClaimAuthorization
}

func New(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.ClientID) == "" || strings.TrimSpace(cfg.RedirectURL) == "" {
		return nil, fmt.Errorf("client_id and redirect_url are required")
	}
	for name, raw := range map[string]string{
		"authorization_url": cfg.AuthorizationURL,
		"token_url":         cfg.TokenURL,
		"userinfo_url":      cfg.UserInfoURL,
		"redirect_url":      cfg.RedirectURL,
	} {
		if err := requireSecureURL(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if !contains(cfg.Scopes, "openid") {
		return nil, fmt.Errorf("openid scope is required")
	}
	if cfg.LoginTTL <= 0 {
		cfg.LoginTTL = 10 * time.Minute
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.ClaimTTL <= 0 {
		cfg.ClaimTTL = 5 * time.Minute
	}

	client := http.Client{Timeout: 10 * time.Second}
	if cfg.HTTPClient != nil {
		client = *cfg.HTTPClient
		if client.Timeout <= 0 {
			client.Timeout = 10 * time.Second
		}
	}
	// Token and UserInfo redirects are rejected rather than followed. This avoids
	// forwarding bearer/client credentials to an unexpected redirect target.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Service{
		cfg:      cfg,
		client:   &client,
		now:      func() time.Time { return time.Now().UTC() },
		logins:   make(map[string]loginTransaction),
		sessions: make(map[string]sessionRecord),
		claims:   make(map[string]ClaimAuthorization),
	}, nil
}

func (s *Service) BeginLogin() (string, error) {
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomToken(48)
	if err != nil {
		return "", err
	}

	now := s.now()
	s.mu.Lock()
	s.pruneLocked(now)
	s.logins[state] = loginTransaction{
		Verifier:  verifier,
		ExpiresAt: now.Add(s.cfg.LoginTTL),
	}
	s.mu.Unlock()

	parsed, _ := url.Parse(s.cfg.AuthorizationURL)
	query := parsed.Query()
	query.Set("response_type", "code")
	query.Set("client_id", s.cfg.ClientID)
	query.Set("redirect_uri", s.cfg.RedirectURL)
	query.Set("scope", strings.Join(s.cfg.Scopes, " "))
	query.Set("state", state)
	query.Set("code_challenge_method", "S256")
	query.Set("code_challenge", pkceChallenge(verifier))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (s *Service) CompleteLogin(ctx context.Context, state, code string) (string, string, Session, error) {
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	if state == "" || code == "" {
		return "", "", Session{}, ErrInvalidState
	}

	now := s.now()
	s.mu.Lock()
	txn, ok := s.logins[state]
	delete(s.logins, state) // OAuth state is one-time even when provider exchange fails.
	s.mu.Unlock()
	if !ok || !txn.ExpiresAt.After(now) {
		return "", "", Session{}, ErrInvalidState
	}

	accessToken, err := s.exchangeCode(ctx, code, txn.Verifier)
	if err != nil {
		return "", "", Session{}, err
	}
	userID, err := s.fetchSubject(ctx, accessToken)
	if err != nil {
		return "", "", Session{}, err
	}
	rawSession, err := randomToken(32)
	if err != nil {
		return "", "", Session{}, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return "", "", Session{}, err
	}

	session := Session{UserID: userID, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	s.mu.Lock()
	s.pruneLocked(now)
	s.sessions[tokenKey(rawSession)] = sessionRecord{
		Session:  session,
		CSRFHash: sha256.Sum256([]byte(csrf)),
	}
	s.mu.Unlock()
	return rawSession, csrf, session, nil
}

func (s *Service) Authenticate(rawSession string) (Session, error) {
	return s.authenticate(rawSession, "", false)
}

func (s *Service) AuthenticateMutation(rawSession, csrf string) (Session, error) {
	return s.authenticate(rawSession, csrf, true)
}

func (s *Service) Logout(rawSession, csrf string) error {
	if _, err := s.AuthenticateMutation(rawSession, csrf); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.sessions, tokenKey(rawSession))
	s.mu.Unlock()
	return nil
}

func (s *Service) MintClaimAuthorization(rawSession, csrf, bootstrapID string) (string, ClaimAuthorization, error) {
	session, err := s.AuthenticateMutation(rawSession, csrf)
	if err != nil {
		return "", ClaimAuthorization{}, err
	}
	bootstrapID = strings.TrimSpace(bootstrapID)
	if bootstrapID == "" || len(bootstrapID) > 128 {
		return "", ClaimAuthorization{}, fmt.Errorf("bootstrap_id required and must be <=128 bytes")
	}

	raw, err := randomToken(32)
	if err != nil {
		return "", ClaimAuthorization{}, err
	}
	claim := ClaimAuthorization{
		UserID:      session.UserID,
		BootstrapID: bootstrapID,
		ExpiresAt:   s.now().Add(s.cfg.ClaimTTL),
	}
	s.mu.Lock()
	s.pruneLocked(s.now())
	s.claims[tokenKey(raw)] = claim
	s.mu.Unlock()
	return raw, claim, nil
}

func (s *Service) ConsumeClaimAuthorization(raw string) (ClaimAuthorization, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ClaimAuthorization{}, ErrInvalidClaim
	}
	key := tokenKey(raw)
	s.mu.Lock()
	claim, ok := s.claims[key]
	delete(s.claims, key) // Consume once even if the caller's later transaction fails.
	s.mu.Unlock()
	if !ok || !claim.ExpiresAt.After(s.now()) {
		return ClaimAuthorization{}, ErrInvalidClaim
	}
	return claim, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/owner/auth/login", s.handleLogin)
	mux.HandleFunc("GET /v1/owner/auth/callback", s.handleCallback)
	mux.HandleFunc("GET /v1/owner/session", s.handleSession)
	mux.HandleFunc("POST /v1/owner/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /v1/owner/claim-authorizations", s.handleClaimAuthorization)
	return mux
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	target, err := s.BeginLogin()
	if err != nil {
		http.Error(w, "owner login unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	raw, csrf, session, err := s.CompleteLogin(
		r.Context(),
		r.URL.Query().Get("state"),
		r.URL.Query().Get("code"),
	)
	if err != nil {
		http.Error(w, "owner login failed", http.StatusUnauthorized)
		return
	}
	setSessionCookies(w, raw, csrf, session.ExpiresAt)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user_id":    session.UserID,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.Authenticate(sessionCookie(r))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user_id":    session.UserID,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.Logout(sessionCookie(r), r.Header.Get("X-CSRF-Token")); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleClaimAuthorization(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BootstrapID string `json:"bootstrap_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	raw, claim, err := s.MintClaimAuthorization(
		sessionCookie(r),
		r.Header.Get("X-CSRF-Token"),
		request.BootstrapID,
	)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"claim_authorization": raw,
		"expires_at":          claim.ExpiresAt,
	})
}

func (s *Service) exchangeCode(ctx context.Context, code, verifier string) (string, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.cfg.RedirectURL},
		"client_id":     {s.cfg.ClientID},
		"code_verifier": {verifier},
	}
	if s.cfg.ClientSecret != "" {
		values.Set("client_secret", s.cfg.ClientSecret)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.cfg.TokenURL,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("oauth token exchange: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("oauth token exchange status %d", response.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumProviderResponseBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode oauth token response: %w", err)
	}
	if strings.TrimSpace(payload.AccessToken) == "" ||
		(payload.TokenType != "" && !strings.EqualFold(payload.TokenType, "bearer")) {
		return "", fmt.Errorf("oauth token response missing bearer access token")
	}
	return payload.AccessToken, nil
}

func (s *Service) fetchSubject(ctx context.Context, accessToken string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.UserInfoURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("oidc userinfo: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("oidc userinfo status %d", response.StatusCode)
	}

	var payload struct {
		Subject string `json:"sub"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maximumProviderResponseBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode oidc userinfo: %w", err)
	}
	payload.Subject = strings.TrimSpace(payload.Subject)
	if payload.Subject == "" || len(payload.Subject) > 256 {
		return "", fmt.Errorf("oidc userinfo missing valid sub")
	}
	return payload.Subject, nil
}

func (s *Service) authenticate(rawSession, csrf string, mutation bool) (Session, error) {
	rawSession = strings.TrimSpace(rawSession)
	if rawSession == "" {
		return Session{}, ErrUnauthorized
	}

	now := s.now()
	key := tokenKey(rawSession)
	s.mu.Lock()
	record, ok := s.sessions[key]
	if ok && !record.ExpiresAt.After(now) {
		delete(s.sessions, key)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return Session{}, ErrUnauthorized
	}

	if mutation {
		csrf = strings.TrimSpace(csrf)
		if csrf == "" {
			return Session{}, ErrUnauthorized
		}
		provided := sha256.Sum256([]byte(csrf))
		if subtle.ConstantTimeCompare(provided[:], record.CSRFHash[:]) != 1 {
			return Session{}, ErrUnauthorized
		}
	}
	return record.Session, nil
}

func (s *Service) pruneLocked(now time.Time) {
	for key, txn := range s.logins {
		if !txn.ExpiresAt.After(now) {
			delete(s.logins, key)
		}
	}
	for key, session := range s.sessions {
		if !session.ExpiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
	for key, claim := range s.claims {
		if !claim.ExpiresAt.After(now) {
			delete(s.claims, key)
		}
	}
}

func setSessionCookies(w http.ResponseWriter, session, csrf string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session,
		Path:     "/",
		Expires:  expires,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrf,
		Path:     "/",
		Expires:  expires,
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteStrictMode,
	})
}

func sessionCookie(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func requireSecureURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("valid absolute URL required")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("URL userinfo and fragments are not allowed")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return fmt.Errorf("https required outside loopback")
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func tokenKey(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
