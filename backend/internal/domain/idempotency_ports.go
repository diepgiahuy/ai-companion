package domain

import (
	"context"
	"time"

	"companion-server/internal/idempotency"
)

type DurableMutationRepository interface {
	CreateExpenseMutation(context.Context, idempotency.Request, string, int64, string, string, time.Time) error
	CreateExpensesMutation(context.Context, idempotency.Request, string, []ExpenseInput) error
	UpdateExpenseMutation(context.Context, idempotency.Request, string, int64, int64, string, string, time.Time) error
	DeleteExpenseMutation(context.Context, idempotency.Request, string, int64) error
	SetBudgetMutation(context.Context, idempotency.Request, string, string, int64) error
	DeleteBudgetMutation(context.Context, idempotency.Request, string, string) error
	CreateNoteMutation(context.Context, idempotency.Request, string, string) error
	UpdateNoteMutation(context.Context, idempotency.Request, string, int64, string) error
	DeleteNoteMutation(context.Context, idempotency.Request, string, int64) error
	CreateJournalMutation(context.Context, idempotency.Request, string, string, time.Time) error
	UpdateJournalMutation(context.Context, idempotency.Request, string, int64, string, time.Time) error
	DeleteJournalMutation(context.Context, idempotency.Request, string, int64) error
	CreateReminderMutation(context.Context, idempotency.Request, string, string, string, time.Time) (ScheduledItem, error)
	CreateTimerMutation(context.Context, idempotency.Request, string, string, string, time.Time) (ScheduledItem, error)
	UpdateScheduledMutation(context.Context, idempotency.Request, string, int64, string, time.Time) error
	PauseTimerMutation(context.Context, idempotency.Request, string, int64, time.Time) error
	ResumeTimerMutation(context.Context, idempotency.Request, string, int64, time.Time) error
	CancelScheduledMutation(context.Context, idempotency.Request, string, int64) error
	DeleteScheduledMutation(context.Context, idempotency.Request, string, int64) error
}

type DurableRepositories interface {
	Repositories
	DurableMutationRepository
}
