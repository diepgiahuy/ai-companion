package events

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeOutbox struct {
	pending      []Pending
	claimErr     error
	markErr      map[int64]error
	retryErr     map[int64]error
	marked       []int64
	retried      []int64
	retryAt      map[int64]time.Time
}

func (f *fakeOutbox) Enqueue(context.Context, Event) error { return nil }
func (f *fakeOutbox) Claim(context.Context, time.Time, int) ([]Pending, error) {
	return append([]Pending(nil), f.pending...), f.claimErr
}
func (f *fakeOutbox) MarkSent(_ context.Context, id int64) error {
	f.marked = append(f.marked, id)
	return f.markErr[id]
}
func (f *fakeOutbox) Retry(_ context.Context, id int64, _ string, next time.Time) error {
	f.retried = append(f.retried, id)
	if f.retryAt == nil {
		f.retryAt = map[int64]time.Time{}
	}
	f.retryAt[id] = next
	return f.retryErr[id]
}

func TestDispatchSurfacesPersistenceFailuresWithoutAbandoningBatch(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	repo := &fakeOutbox{
		pending: []Pending{
			{RowID: 1, Event: Event{ID: "handler-fails"}, Attempts: 2},
			{RowID: 2, Event: Event{ID: "mark-fails"}},
			{RowID: 3, Event: Event{ID: "ok"}},
		},
		markErr:  map[int64]error{2: errors.New("write sent failed")},
		retryErr: map[int64]error{1: errors.New("write retry failed")},
	}

	err := Dispatch(context.Background(), repo, func(_ context.Context, event Event) error {
		if event.ID == "handler-fails" {
			return errors.New("delivery failed")
		}
		return nil
	}, now, 20)
	if err == nil {
		t.Fatal("expected persistence errors")
	}
	message := err.Error()
	if !strings.Contains(message, "retry outbox row 1") || !strings.Contains(message, "mark outbox row 2 sent") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.retried) != 1 || repo.retried[0] != 1 {
		t.Fatalf("retried = %v", repo.retried)
	}
	wantRetry := now.Add(4 * time.Second)
	if got := repo.retryAt[1]; !got.Equal(wantRetry) {
		t.Fatalf("retry time = %v, want %v", got, wantRetry)
	}
	if len(repo.marked) != 2 || repo.marked[0] != 2 || repo.marked[1] != 3 {
		t.Fatalf("marked = %v; later batch item was not processed", repo.marked)
	}
}

func TestDispatchWrapsClaimFailure(t *testing.T) {
	repo := &fakeOutbox{claimErr: errors.New("database unavailable")}
	err := Dispatch(context.Background(), repo, func(context.Context, Event) error { return nil }, time.Now(), 20)
	if err == nil || !strings.Contains(err.Error(), "claim outbox events") {
		t.Fatalf("unexpected error: %v", err)
	}
}
