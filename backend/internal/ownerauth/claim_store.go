package ownerauth

import (
	"context"
	"sync"
	"time"
)

// ClaimCodeStore abstracts rate limiting and redemption persistence for
// human claim codes, supporting both in-memory and PostgreSQL storage.
type ClaimCodeStore interface {
	AllowAttempt(ctx context.Context, host string, limit int, window time.Duration) (bool, error)
	GetRedemption(ctx context.Context, codeKey, binding, redemptionKey string) (rawAuthorization string, claim ClaimAuthorization, found bool, err error)
	PutRedemption(ctx context.Context, codeKey, redemptionKey, rawAuthorization string, claim ClaimAuthorization) error
}

type MemoryClaimCodeStore struct {
	mu          sync.Mutex
	attempts    map[string]claimCodeAttemptWindow
	redemptions map[string]claimCodeRedemption
}

func NewMemoryClaimCodeStore() *MemoryClaimCodeStore {
	return &MemoryClaimCodeStore{
		attempts:    make(map[string]claimCodeAttemptWindow),
		redemptions: make(map[string]claimCodeRedemption),
	}
}

func (m *MemoryClaimCodeStore) AllowAttempt(_ context.Context, host string, limit int, window time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for key, w := range m.attempts {
		if now.Sub(w.started) >= window {
			delete(m.attempts, key)
		}
	}
	w := m.attempts[host]
	if w.started.IsZero() || now.Sub(w.started) >= window {
		m.attempts[host] = claimCodeAttemptWindow{started: now, count: 1}
		return true, nil
	}
	if w.count >= limit {
		return false, nil
	}
	w.count++
	m.attempts[host] = w
	return true, nil
}

func (m *MemoryClaimCodeStore) GetRedemption(_ context.Context, codeKey, binding, redemptionKey string) (string, ClaimAuthorization, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for key, entry := range m.redemptions {
		if !entry.claim.ExpiresAt.After(now) {
			delete(m.redemptions, key)
		}
	}
	entry, ok := m.redemptions[codeKey]
	if !ok || entry.claim.BootstrapID != binding || entry.redemptionKey != redemptionKey {
		return "", ClaimAuthorization{}, false, nil
	}
	return entry.rawAuthorization, entry.claim, true, nil
}

func (m *MemoryClaimCodeStore) PutRedemption(_ context.Context, codeKey, redemptionKey, rawAuthorization string, claim ClaimAuthorization) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.redemptions[codeKey] = claimCodeRedemption{
		redemptionKey:    redemptionKey,
		rawAuthorization: rawAuthorization,
		claim:            claim,
	}
	return nil
}
