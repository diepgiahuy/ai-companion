package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerLoginLimiterBoundsOneHostAndResets(t *testing.T) {
	limiter := newOwnerIngressLimiter(ownerLoginPerHostPerMinute, ownerLoginGlobalPerMinute)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < ownerLoginPerHostPerMinute; i++ {
		if !limiter.allow("192.0.2.10:12345", now) {
			t.Fatalf("attempt %d unexpectedly rejected", i+1)
		}
	}
	if limiter.allow("192.0.2.10:54321", now) {
		t.Fatal("per-host budget did not reject excess login")
	}
	if !limiter.allow("192.0.2.10:54321", now.Add(time.Minute)) {
		t.Fatal("per-host budget did not reset after one minute")
	}
}

func TestOwnerLoginLimiterHasGlobalBoundAcrossHosts(t *testing.T) {
	limiter := newOwnerIngressLimiter(ownerLoginPerHostPerMinute, ownerLoginGlobalPerMinute)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < ownerLoginGlobalPerMinute; i++ {
		remote := fmt.Sprintf("198.51.%d.%d:443", i/250, i%250+1)
		if !limiter.allow(remote, now) {
			t.Fatalf("global attempt %d unexpectedly rejected", i+1)
		}
	}
	if limiter.allow("203.0.113.2:1", now) {
		t.Fatal("global budget did not reject excess login")
	}
}

func TestLimitOwnerLoginsReturnsRetryAfter(t *testing.T) {
	called := 0
	handler := limitOwnerLogins(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < ownerLoginPerHostPerMinute; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/owner/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:4000"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status=%d", i+1, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/owner/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:5000"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") != "60" {
		t.Fatalf("status=%d retry-after=%q", res.Code, res.Header().Get("Retry-After"))
	}
	if called != ownerLoginPerHostPerMinute {
		t.Fatalf("downstream calls=%d want=%d", called, ownerLoginPerHostPerMinute)
	}
}

func TestOwnerLoginLimiterDoesNotRateLimitOtherOwnerRoutes(t *testing.T) {
	called := 0
	handler := limitOwnerLogins(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < ownerLoginPerHostPerMinute+10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/owner/session", nil)
		req.RemoteAddr = "203.0.113.8:4000"
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("session route status=%d", res.Code)
		}
	}
	if called != ownerLoginPerHostPerMinute+10 {
		t.Fatalf("downstream calls=%d", called)
	}
}

func TestClaimCodeRedeemIngressHasGlobalBoundAcrossHosts(t *testing.T) {
	called := 0
	handler := limitOwnerClaimCodeRedeems(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < ownerRedeemGlobalPerMinute; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-codes/redeem", nil)
		req.RemoteAddr = fmt.Sprintf("192.0.%d.%d:5000", i/250, i%250+1)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusNoContent {
			t.Fatalf("redeem attempt %d status=%d", i+1, res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/owner/device-claim-codes/redeem", nil)
	req.RemoteAddr = "203.0.113.90:5000"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusTooManyRequests || res.Header().Get("Retry-After") != "60" {
		t.Fatalf("status=%d retry-after=%q", res.Code, res.Header().Get("Retry-After"))
	}
	if called != ownerRedeemGlobalPerMinute {
		t.Fatalf("downstream calls=%d want=%d", called, ownerRedeemGlobalPerMinute)
	}
}
