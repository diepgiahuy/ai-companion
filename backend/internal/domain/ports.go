package domain

import (
	"context"
	"time"
)

type NoteRepository interface {
	CreateNote(ctx context.Context, userID, key, content string) error
	ListNotes(ctx context.Context, userID string, limit int) ([]Note, error)
	UpdateNote(ctx context.Context, userID string, id int64, content string) error
	DeleteNote(ctx context.Context, userID string, id int64) error
}

type ExpenseRepository interface {
	CreateExpense(ctx context.Context, userID, key string, amount int64, category, description string, occurredAt time.Time) error
	CreateExpenses(ctx context.Context, userID, key string, items []ExpenseInput) error
	ExpenseTotal(ctx context.Context, userID string, from, to time.Time) (int64, error)
	ListExpenses(ctx context.Context, userID string, from, to time.Time, category string, limit int) ([]Expense, error)
	UpdateExpense(ctx context.Context, userID string, id int64, amount int64, category, description string, occurredAt time.Time) error
	DeleteExpense(ctx context.Context, userID string, id int64) error
}

type BudgetRepository interface {
	SetBudget(ctx context.Context, userID, period string, limit int64) error
	BudgetLimit(ctx context.Context, userID, period string) (int64, bool, error)
	DeleteBudget(ctx context.Context, userID, period string) error
}

type JournalRepository interface {
	CreateJournal(ctx context.Context, userID, key, content string, occurredAt time.Time) error
	ListJournal(ctx context.Context, userID string, from, to time.Time, limit int) ([]JournalEntry, error)
	UpdateJournal(ctx context.Context, userID string, id int64, content string, occurredAt time.Time) error
	DeleteJournal(ctx context.Context, userID string, id int64) error
}

type ScheduleRepository interface {
	CreateReminderForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error
	CreateTimerForDevice(ctx context.Context, userID, key, deviceID, title string, fireAt time.Time) error
	ListReminders(ctx context.Context, userID, deviceID, status string, limit int) ([]ScheduledItem, error)
	ListTimers(ctx context.Context, userID, deviceID, status string, limit int) ([]ScheduledItem, error)
	UpdateScheduledItem(ctx context.Context, userID string, id int64, title string, fireAt time.Time) error
	PauseTimer(ctx context.Context, userID string, id int64, now time.Time) error
	ResumeTimer(ctx context.Context, userID string, id int64, now time.Time) error
	CancelScheduledItem(ctx context.Context, userID string, id int64) error
	DeleteScheduledItem(ctx context.Context, userID string, id int64) error
}

type VoiceMemoRepository interface {
	CreateVoiceMemo(ctx context.Context, userID, key, deviceID, path, transcript string, durationMS int64) error
	VoiceMemoByKey(ctx context.Context, userID, key string) (VoiceMemo, bool, error)
	VoiceMemoByID(ctx context.Context, userID string, id int64) (VoiceMemo, bool, error)
	ListVoiceMemos(ctx context.Context, userID, deviceID string, limit int) ([]VoiceMemo, error)
	DeleteVoiceMemo(ctx context.Context, userID string, id int64) error
}

type ReadRepositories interface {
	NoteRepository
	ExpenseRepository
	BudgetRepository
	JournalRepository
	ScheduleRepository
	VoiceMemoRepository
}

type Repositories interface{ ReadRepositories }
