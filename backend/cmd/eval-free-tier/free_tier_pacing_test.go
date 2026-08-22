package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingTransport struct {
	mu     sync.Mutex
	starts []time.Time
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.starts = append(t.starts, time.Now())
	t.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    req,
	}, nil
}

func (t *recordingTransport) snapshot() []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]time.Time(nil), t.starts...)
}

func TestPacedInferenceTransportSpacesRequestStarts(t *testing.T) {
	base := &recordingTransport{}
	transport := &pacedInferenceTransport{base: base, minInterval: 40 * time.Millisecond}
	client := &http.Client{Transport: transport}

	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, "https://example.test/models/gemma:streamGenerateContent", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	starts := base.snapshot()
	if len(starts) != 2 {
		t.Fatalf("request starts=%d want=2", len(starts))
	}
	if delta := starts[1].Sub(starts[0]); delta < 35*time.Millisecond {
		t.Fatalf("request spacing=%s want>=35ms", delta)
	}
}

func TestPacedInferenceTransportCancellationStopsBeforeNetwork(t *testing.T) {
	base := &recordingTransport{}
	transport := &pacedInferenceTransport{base: base, minInterval: 250 * time.Millisecond}
	client := &http.Client{Transport: transport}

	first, _ := http.NewRequest(http.MethodPost, "https://example.test/models/gemma:streamGenerateContent", nil)
	resp, err := client.Do(first)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	second, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.test/models/gemma:streamGenerateContent", nil)
	_, err = client.Do(second)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want deadline exceeded", err)
	}
	if got := len(base.snapshot()); got != 1 {
		t.Fatalf("network requests=%d want=1", got)
	}
}

func TestPacedInferenceTransportDoesNotDelayMetadataOrDefaultZero(t *testing.T) {
	base := &recordingTransport{}
	transport := &pacedInferenceTransport{base: base, minInterval: 200 * time.Millisecond}
	client := &http.Client{Transport: transport}

	metadata, _ := http.NewRequest(http.MethodGet, "https://example.test/models/gemma", nil)
	resp, err := client.Do(metadata)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	zero := &pacedInferenceTransport{base: base}
	zeroClient := &http.Client{Transport: zero}
	inference, _ := http.NewRequest(http.MethodPost, "https://example.test/models/gemma:streamGenerateContent", nil)
	start := time.Now()
	resp, err = zeroClient.Do(inference)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("zero pacing unexpectedly delayed request by %s", elapsed)
	}
}
