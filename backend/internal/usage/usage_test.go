package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockTotalReader struct {
	total int64
	err   error
	user  string
	since time.Time
}

func (m *mockTotalReader) TotalTokensSince(_ context.Context, user string, since time.Time) (int64, error) {
	m.user = user
	m.since = since
	if m.err != nil {
		return 0, m.err
	}
	return m.total, nil
}

func TestMemoryMeterConcurrency(t *testing.T) {
	meter := NewMemory()
	var wg sync.WaitGroup
	workers := 20
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				meter.RecordUsage(context.Background(), Record{
					Provider:         "openai",
					Model:            "gpt-4o-mini",
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
					UserID:           "user-1",
					DeviceID:         "dev-1",
				})
			}
		}(i)
	}
	wg.Wait()

	snap := meter.Snapshot()
	expectedTotal := workers * iterations * 15
	if snap["total_tokens"] != expectedTotal {
		t.Fatalf("expected total_tokens=%d, got %d", expectedTotal, snap["total_tokens"])
	}
	expectedPrompt := workers * iterations * 10
	if snap["prompt_tokens"] != expectedPrompt {
		t.Fatalf("expected prompt_tokens=%d, got %d", expectedPrompt, snap["prompt_tokens"])
	}
	expectedComp := workers * iterations * 5
	if snap["completion_tokens"] != expectedComp {
		t.Fatalf("expected completion_tokens=%d, got %d", expectedComp, snap["completion_tokens"])
	}
}

func TestGuardLimits(t *testing.T) {
	fixedNow := time.Date(2026, 8, 20, 15, 30, 0, 0, time.UTC)
	expectedSince := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		monthlyLimit int64
		mockTotal    int64
		mockErr      error
		nilReader    bool
		wantErr      bool
	}{
		{
			name:         "no limit configured (unlimited)",
			monthlyLimit: 0,
			mockTotal:    500000,
			wantErr:      false,
		},
		{
			name:         "nil reader -> no-op check",
			monthlyLimit: 10000,
			nilReader:    true,
			wantErr:      false,
		},
		{
			name:         "under quota -> allowed",
			monthlyLimit: 10000,
			mockTotal:    9999,
			wantErr:      false,
		},
		{
			name:         "exact limit reached -> quota exceeded error",
			monthlyLimit: 10000,
			mockTotal:    10000,
			wantErr:      true,
		},
		{
			name:         "over limit -> quota exceeded error",
			monthlyLimit: 10000,
			mockTotal:    12000,
			wantErr:      true,
		},
		{
			name:         "reader returns error -> propagates error",
			monthlyLimit: 10000,
			mockErr:      errors.New("db connection lost"),
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reader TotalReader
			var mock *mockTotalReader
			if !tt.nilReader {
				mock = &mockTotalReader{total: tt.mockTotal, err: tt.mockErr}
				reader = mock
			}

			guard := Guard{
				Reader:       reader,
				MonthlyLimit: tt.monthlyLimit,
				Now:          func() time.Time { return fixedNow },
			}

			err := guard.Check(context.Background(), "user-test-123")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Check() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if mock != nil && tt.monthlyLimit > 0 {
				if mock.user != "user-test-123" {
					t.Errorf("expected user 'user-test-123', got %q", mock.user)
				}
				if !mock.since.Equal(expectedSince) {
					t.Errorf("expected since=%v, got %v", expectedSince, mock.since)
				}
			}
		})
	}
}
