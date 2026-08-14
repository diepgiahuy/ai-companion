package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/idempotency"
)

type DurableRepository interface {
	UpsertMemoryMutation(context.Context, idempotency.Request, Item) (Item, error)
	ForgetMemoryMutation(context.Context, idempotency.Request, string, string) error
}

func (s *Service) RememberMutation(ctx context.Context, request idempotency.Request, user, key string, kind Kind, value, source string, confidence float64, validFrom time.Time) (Item, error) {
	if validFrom.IsZero() {
		validFrom = s.now()
	}
	if confidence <= 0 {
		confidence = 1
	}
	if source == "" {
		source = "user"
	}
	repo, ok := s.repo.(DurableRepository)
	if !ok {
		return Item{}, fmt.Errorf("durable memory repository is unavailable")
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	emb, err := s.embed.Embed(ctx, key+" "+value)
	if err != nil {
		return Item{}, err
	}
	m, err := repo.UpsertMemoryMutation(ctx, request, Item{UserID: user, Key: key, Kind: kind, Value: value, ValidFrom: validFrom, Source: source, Confidence: confidence, CreatedAt: s.now()})
	if err != nil {
		return Item{}, err
	}
	// Vector state is secondary and rebuildable. Re-applying this on a replay
	// repairs a missing projection without changing authoritative memory state.
	if s.vectors != nil {
		_ = s.vectors.UpsertVector(ctx, user, m.ID, emb)
	}
	return m, nil
}

func (s *Service) ForgetMutation(ctx context.Context, request idempotency.Request, user, key string) error {
	repo, ok := s.repo.(DurableRepository)
	if !ok {
		return fmt.Errorf("durable memory repository is unavailable")
	}
	return repo.ForgetMemoryMutation(ctx, request, user, strings.TrimSpace(key))
}
