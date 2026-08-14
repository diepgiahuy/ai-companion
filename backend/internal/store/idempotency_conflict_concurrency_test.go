package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"companion-server/internal/idempotency"
)

func TestIdempotentMutationConcurrentConflictsCannotBothCommit(t *testing.T) {
	ctx := context.Background()
	s := openIdemTestStore(t, filepath.Join(t.TempDir(), "conflicting-concurrent.db"))
	defer s.Close()

	first := idemRequest(t, "actor-a", "shared-key", `{"value":"one"}`)
	second := idemRequest(t, "actor-a", "shared-key", `{"value":"two"}`)

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	run := func(request idempotency.Request, value string) {
		defer wg.Done()
		<-start
		_, err := s.runIdempotentMutation(ctx, request, func(tx sqlTx) (any, error) {
			_, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-a", value)
			return map[string]any{"value": value}, err
		})
		errs <- err
	}

	wg.Add(2)
	go run(first, "one")
	go run(second, "two")
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case idempotency.IsConflict(err):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d; want one commit and one IDEMPOTENCY_CONFLICT", successes, conflicts)
	}

	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM idem_test_values`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("committed rows=%d; conflicting retries both mutated state", rows)
	}
}

// sqlTx is the minimal transaction surface used by the concurrency fixture.
// *sql.Tx satisfies it; keeping this local avoids widening production APIs.
type sqlTx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
