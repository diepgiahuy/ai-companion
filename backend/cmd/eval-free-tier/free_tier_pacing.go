package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const freeTierPacingEnv = "COMPANION_FREE_TIER_MIN_REQUEST_INTERVAL"

// pacedInferenceTransport is benchmark-only request pacing. It deliberately
// spaces inference request starts without retrying provider failures. Metadata
// GETs and unrelated HTTP traffic are not delayed.
type pacedInferenceTransport struct {
	base        http.RoundTripper
	minInterval time.Duration

	mu        sync.Mutex
	lastStart time.Time
}

func init() {
	raw := strings.TrimSpace(os.Getenv(freeTierPacingEnv))
	if raw == "" {
		return
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < 0 {
		http.DefaultTransport = errorInferenceTransport{
			base: http.DefaultTransport,
			err:  fmt.Errorf("invalid %s=%q: expected a non-negative Go duration", freeTierPacingEnv, raw),
		}
		return
	}
	if interval == 0 {
		return
	}
	http.DefaultTransport = &pacedInferenceTransport{
		base:        http.DefaultTransport,
		minInterval: interval,
	}
}

func (t *pacedInferenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isGemmaInferenceRequest(req) || t.minInterval <= 0 {
		return t.transport().RoundTrip(req)
	}
	if err := t.wait(req.Context()); err != nil {
		return nil, err
	}
	return t.transport().RoundTrip(req)
}

func (t *pacedInferenceTransport) wait(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastStart.IsZero() {
		t.lastStart = time.Now()
		return nil
	}
	wait := time.Until(t.lastStart.Add(t.minInterval))
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	t.lastStart = time.Now()
	return nil
}

func (t *pacedInferenceTransport) transport() http.RoundTripper {
	if t.base != nil {
		return t.base
	}
	return http.DefaultTransport
}

func isGemmaInferenceRequest(req *http.Request) bool {
	return req != nil && req.Method == http.MethodPost && req.URL != nil && strings.Contains(req.URL.Path, ":streamGenerateContent")
}

type errorInferenceTransport struct {
	base http.RoundTripper
	err  error
}

func (t errorInferenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if isGemmaInferenceRequest(req) {
		return nil, t.err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
