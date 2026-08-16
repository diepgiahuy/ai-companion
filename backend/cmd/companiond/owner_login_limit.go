package main

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ownerLoginPerHostPerMinute       = 30
	ownerLoginGlobalPerMinute        = 120
	ownerRedeemPerHostPerMinute      = 12
	ownerRedeemGlobalPerMinute       = 120
)

type ownerIngressWindow struct {
	started time.Time
	count   int
}

type ownerIngressLimiter struct {
	mu          sync.Mutex
	perHost     map[string]ownerIngressWindow
	global      ownerIngressWindow
	perHostMax  int
	globalMax   int
}

func newOwnerIngressLimiter(perHostMax, globalMax int) *ownerIngressLimiter {
	return &ownerIngressLimiter{
		perHost:    make(map[string]ownerIngressWindow),
		perHostMax: perHostMax,
		globalMax:  globalMax,
	}
}

func ownerIngressHost(remote string) string {
	remote = strings.TrimSpace(remote)
	host, _, err := net.SplitHostPort(remote)
	if err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	if remote == "" {
		return "unknown"
	}
	return remote
}

func resetOwnerIngressWindow(window ownerIngressWindow, now time.Time) bool {
	return window.started.IsZero() || now.Sub(window.started) >= time.Minute || now.Before(window.started)
}

func (l *ownerIngressLimiter) allow(remote string, now time.Time) bool {
	if l == nil || l.perHostMax <= 0 || l.globalMax <= 0 {
		return false
	}
	host := ownerIngressHost(remote)
	l.mu.Lock()
	defer l.mu.Unlock()

	for key, window := range l.perHost {
		if resetOwnerIngressWindow(window, now) {
			delete(l.perHost, key)
		}
	}
	if resetOwnerIngressWindow(l.global, now) {
		l.global = ownerIngressWindow{started: now}
	}
	if l.global.count >= l.globalMax {
		return false
	}

	window, ok := l.perHost[host]
	if !ok || resetOwnerIngressWindow(window, now) {
		window = ownerIngressWindow{started: now}
	}
	if window.count >= l.perHostMax {
		return false
	}
	window.count++
	l.perHost[host] = window
	l.global.count++
	return true
}

func limitOwnerRoute(next http.Handler, path, message string, perHostMax, globalMax int) http.Handler {
	limiter := newOwnerIngressLimiter(perHostMax, globalMax)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path && !limiter.allow(r.RemoteAddr, time.Now().UTC()) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, message, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitOwnerLogins bounds unauthenticated OAuth state/PKCE creation before it
// reaches ownerauth.Service. The service still owns TTL pruning and one-time
// state semantics; this ingress guard prevents a request flood from growing the
// process-local pending-login map without bound during that TTL.
func limitOwnerLogins(next http.Handler) http.Handler {
	return limitOwnerRoute(
		next,
		"/v1/owner/auth/login",
		"too many owner login attempts",
		ownerLoginPerHostPerMinute,
		ownerLoginGlobalPerMinute,
	)
}

// The claim-code service already has a stricter per-client attempt limiter.
// This outer global bound exists for a different reason: without it, a flood of
// distinct source IPs can grow the service's process-local per-IP limiter map
// throughout its one-minute window and make every request prune an unbounded
// set. RemoteAddr is deliberate; client-controlled forwarding headers are not
// trusted at this security boundary.
func limitOwnerClaimCodeRedeems(next http.Handler) http.Handler {
	return limitOwnerRoute(
		next,
		"/v1/owner/device-claim-codes/redeem",
		"too many claim-code redemption attempts",
		ownerRedeemPerHostPerMinute,
		ownerRedeemGlobalPerMinute,
	)
}
