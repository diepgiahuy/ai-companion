package market

import (
	"context"
	"fmt"
	"time"
)

type Watch struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id,omitempty"`
	DeviceID  string    `json:"device_id,omitempty"`
	Provider  string    `json:"provider"`
	Symbol    string    `json:"symbol"`
	Currency  string    `json:"currency"`
	Operator  string    `json:"operator"`
	Threshold float64   `json:"threshold"`
	Enabled   bool      `json:"enabled"`
	LastState bool      `json:"last_state"`
	CreatedAt time.Time `json:"created_at"`
}
type WatchRepository interface {
	CreateMarketWatch(context.Context, string, string, string, string, string, string, string, float64) (Watch, error)
	ListMarketWatches(context.Context, string, string, int) ([]Watch, error)
	DeleteMarketWatch(context.Context, string, int64) error
	EnabledMarketWatches(context.Context, int) ([]Watch, error)
	SetMarketWatchState(context.Context, int64, bool) error
}

func Matches(w Watch, price float64) bool {
	switch w.Operator {
	case "<":
		return price < w.Threshold
	case "<=":
		return price <= w.Threshold
	case ">":
		return price > w.Threshold
	case ">=":
		return price >= w.Threshold
	}
	return false
}
func ValidateOperator(op string) error {
	switch op {
	case "<", "<=", ">", ">=":
		return nil
	}
	return fmt.Errorf("invalid operator")
}
