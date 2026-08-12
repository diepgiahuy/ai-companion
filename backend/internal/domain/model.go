package domain

import "time"

type Identity struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
	Plan     string `json:"plan,omitempty"`
}

type ExpenseInput struct {
	AmountVND   int64
	Category    string
	Description string
	OccurredAt  time.Time
}

type Expense struct {
	ID          int64     `json:"id"`
	UserID      string    `json:"user_id,omitempty"`
	AmountVND   int64     `json:"amount_vnd"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type Note struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type JournalEntry struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id,omitempty"`
	Content    string    `json:"content"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ScheduledItem struct {
	ID                     int64      `json:"id"`
	UserID                 string     `json:"user_id,omitempty"`
	DeviceID               string     `json:"device_id,omitempty"`
	Kind                   string     `json:"kind"`
	Title                  string     `json:"title"`
	FireAt                 time.Time  `json:"fire_at"`
	Status                 string     `json:"status"`
	Attempts               int        `json:"attempts,omitempty"`
	NextAttempt            *time.Time `json:"next_attempt_at,omitempty"`
	PausedRemainingSeconds int64      `json:"paused_remaining_seconds,omitempty"`
}

type VoiceMemo struct {
	ID         int64     `json:"id"`
	UserID     string    `json:"user_id,omitempty"`
	DeviceID   string    `json:"device_id,omitempty"`
	Path       string    `json:"path"`
	Transcript string    `json:"transcript,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	CreatedAt  time.Time `json:"created_at"`
}
