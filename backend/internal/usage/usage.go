package usage

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Record struct {
	Provider         string
	Model            string
	PromptVersion    string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	UserID           string
	DeviceID         string
}
type Meter interface{ RecordUsage(context.Context, Record) }

type MemoryMeter struct {
	mu     sync.Mutex
	Totals map[string]int
}

func NewMemory() *MemoryMeter { return &MemoryMeter{Totals: map[string]int{}} }
func (m *MemoryMeter) RecordUsage(_ context.Context, u Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Totals["prompt_tokens"] += u.PromptTokens
	m.Totals["completion_tokens"] += u.CompletionTokens
	m.Totals["total_tokens"] += u.TotalTokens
}
func (m *MemoryMeter) Snapshot() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := map[string]int{}
	for k, v := range m.Totals {
		o[k] = v
	}
	return o
}

type TotalReader interface {
	TotalTokensSince(context.Context, string, time.Time) (int64, error)
}
type Guard struct {
	Reader       TotalReader
	MonthlyLimit int64
	Now          func() time.Time
}

func (g Guard) Check(ctx context.Context, user string) error {
	if g.MonthlyLimit <= 0 || g.Reader == nil {
		return nil
	}
	now := time.Now()
	if g.Now != nil {
		now = g.Now()
	}
	since := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	total, err := g.Reader.TotalTokensSince(ctx, user, since)
	if err != nil {
		return err
	}
	if total >= g.MonthlyLimit {
		return fmt.Errorf("monthly LLM token quota exceeded")
	}
	return nil
}
