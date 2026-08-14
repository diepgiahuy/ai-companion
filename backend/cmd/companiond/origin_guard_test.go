package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceOriginGuardAllowsNonBrowserDeviceRequest(t *testing.T) {
	t.Setenv("COMPANION_ALLOWED_DEVICE_ORIGINS", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequest(http.MethodGet, "http://server/v2/device", nil)
	w := httptest.NewRecorder()
	deviceOriginGuard(next).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d; want %d", w.Code, http.StatusNoContent)
	}
}

func TestDeviceOriginGuardRejectsBrowserOriginByDefault(t *testing.T) {
	t.Setenv("COMPANION_ALLOWED_DEVICE_ORIGINS", "")
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true; w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequest(http.MethodGet, "http://server/v2/device", nil)
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	deviceOriginGuard(next).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || nextCalled {
		t.Fatalf("status=%d nextCalled=%v; want forbidden before websocket handler", w.Code, nextCalled)
	}
}

func TestDeviceOriginGuardAllowsOnlyExactConfiguredHTTPSOrigin(t *testing.T) {
	t.Setenv("COMPANION_ALLOWED_DEVICE_ORIGINS", "https://companion.example, http://insecure.example, https://bad.example/path")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		origin string
		want   int
	}{
		{"https://companion.example", http.StatusNoContent},
		{"https://companion.example.evil", http.StatusForbidden},
		{"http://insecure.example", http.StatusForbidden},
		{"https://bad.example/path", http.StatusForbidden},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://server/v2/device", nil)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			deviceOriginGuard(next).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d; want %d", w.Code, tc.want)
			}
		})
	}
}

func TestDeviceOriginGuardRejectsMultipleOriginHeaders(t *testing.T) {
	t.Setenv("COMPANION_ALLOWED_DEVICE_ORIGINS", "https://companion.example")
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { nextCalled = true })
	r := httptest.NewRequest(http.MethodGet, "http://server/v2/device", nil)
	r.Header.Add("Origin", "https://companion.example")
	r.Header.Add("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	deviceOriginGuard(next).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || nextCalled {
		t.Fatalf("status=%d nextCalled=%v; multiple Origin headers must fail closed", w.Code, nextCalled)
	}
}

func TestDeviceOriginGuardDoesNotAffectOtherHTTPRoutes(t *testing.T) {
	t.Setenv("COMPANION_ALLOWED_DEVICE_ORIGINS", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequest(http.MethodGet, "http://server/healthz", nil)
	r.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	deviceOriginGuard(next).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d; non-device route should be unchanged", w.Code)
	}
}
