package server

import (
	"errors"
	"fmt"
	"testing"
)

func TestProcessInboundReplayCacheIsSessionLocal(t *testing.T) {
	const messageID = "message-1"
	data := []byte(`{"version":2,"message_id":"message-1","payload":{"value":1}}`)

	calls := 0
	for i := 0; i < 2; i++ {
		s := &session{}
		if err := s.processInbound(messageID, data, func() error {
			calls++
			return nil
		}); err != nil {
			t.Fatalf("session %d processInbound: %v", i, err)
		}
	}

	if calls != 2 {
		t.Fatalf("calls = %d, want 2: replay suppression must not cross sessions", calls)
	}
}

func TestProcessInboundDoesNotCacheFailedAction(t *testing.T) {
	s := &session{}
	const messageID = "message-retry"
	data := []byte(`{"version":2,"message_id":"message-retry","payload":{"value":1}}`)
	calls := 0
	transient := errors.New("temporary repository failure")
	if err := s.processInbound(messageID, data, func() error { calls++; return transient }); !errors.Is(err, transient) {
		t.Fatalf("first error = %v, want transient failure", err)
	}
	if _, exists := s.seenInbound[messageID]; exists {
		t.Fatal("failed action was cached")
	}
	if err := s.processInbound(messageID, data, func() error { calls++; return nil }); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := s.processInbound(messageID, data, func() error { calls++; return nil }); err != nil {
		t.Fatalf("successful replay: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestProcessInboundReplayCacheEvictsOldestAfterBound(t *testing.T) {
	s := &session{}
	const firstID = "message-0"
	firstData := []byte(`{"version":2,"message_id":"message-0","payload":{"value":0}}`)

	firstCalls := 0
	if err := s.processInbound(firstID, firstData, func() error {
		firstCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The live-session replay cache intentionally remembers at most 256 messages.
	// Adding 256 more distinct messages after message-0 must evict message-0.
	for i := 1; i <= 256; i++ {
		id := fmt.Sprintf("message-%d", i)
		data := []byte(fmt.Sprintf(`{"version":2,"message_id":%q,"payload":{"value":%d}}`, id, i))
		if err := s.processInbound(id, data, func() error { return nil }); err != nil {
			t.Fatalf("process %s: %v", id, err)
		}
	}

	if _, exists := s.seenInbound[firstID]; exists {
		t.Fatalf("%s still cached after exceeding the 256-message session window", firstID)
	}

	if err := s.processInbound(firstID, firstData, func() error {
		firstCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 2 {
		t.Fatalf("first message calls = %d, want 2 after eviction", firstCalls)
	}
}
