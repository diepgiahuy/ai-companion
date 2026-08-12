package events

import (
	"context"
	"encoding/json"
	"time"
)

// Event intentionally follows the CloudEvents envelope shape without forcing a transport dependency.
type Event struct {
	ID      string          `json:"id"`
	Source  string          `json:"source"`
	Type    string          `json:"type"`
	Subject string          `json:"subject,omitempty"`
	Time    time.Time       `json:"time"`
	UserID  string          `json:"user_id,omitempty"`
	Data    json.RawMessage `json:"data"`
}
type Pending struct {
	RowID       int64
	Event       Event
	Attempts    int
	NextAttempt time.Time
}
type Outbox interface {
	Enqueue(context.Context, Event) error
	Claim(context.Context, time.Time, int) ([]Pending, error)
	MarkSent(context.Context, int64) error
	Retry(context.Context, int64, string, time.Time) error
}
type Handler func(context.Context, Event) error

func Dispatch(ctx context.Context, repo Outbox, h Handler, now time.Time, limit int) error {
	xs, e := repo.Claim(ctx, now, limit)
	if e != nil {
		return e
	}
	for _, x := range xs {
		if e := h(ctx, x.Event); e != nil {
			_ = repo.Retry(ctx, x.RowID, e.Error(), now.Add(time.Duration(1<<min(x.Attempts, 8))*time.Second))
			continue
		}
		_ = repo.MarkSent(ctx, x.RowID)
	}
	return nil
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
