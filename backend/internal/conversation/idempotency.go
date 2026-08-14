package conversation

import (
	"context"
	"fmt"

	"companion-server/internal/idempotency"
)

type DurableClearStore interface {
	ClearMutation(context.Context, idempotency.Request, Scope) (bool, error)
}

// ClearMutation keeps cache invalidation outside the authoritative database
// transaction while requiring the store to persist clear + idempotency outcome
// atomically. The returned boolean is true on replay.
func (s *Service) ClearMutation(ctx context.Context, request idempotency.Request, scope Scope) (bool, error) {
	store, ok := s.store.(DurableClearStore)
	if !ok {
		return false, fmt.Errorf("durable conversation clear is unavailable")
	}
	replayed, err := store.ClearMutation(ctx, request, scope)
	if err != nil {
		return false, err
	}
	if s.cache != nil {
		s.cache.Invalidate(scope)
	}
	return replayed, nil
}
