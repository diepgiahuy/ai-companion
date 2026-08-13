package tools

import (
	"companion-server/internal/idempotency"
	"companion-server/internal/market"
	"context"
)

type durableMarketWatchRepository interface {
	market.WatchRepository
	CreateMarketWatchMutation(context.Context, idempotency.Request, string, string, string, string, string, string, float64) (market.Watch, error)
	DeleteMarketWatchMutation(context.Context, idempotency.Request, string, int64) error
}
