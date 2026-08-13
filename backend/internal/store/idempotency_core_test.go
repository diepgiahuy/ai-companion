package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"companion-server/internal/idempotency"
)

func openIdemTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil { t.Fatal(err) }
	if err := s.migrateIdempotency(); err != nil { t.Fatal(err) }
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS idem_test_values (actor TEXT NOT NULL,value TEXT NOT NULL)`); err != nil { t.Fatal(err) }
	return s
}

func idemRequest(t *testing.T, actor, key, payload string) idempotency.Request {
	t.Helper()
	hash, err := idempotency.HashJSON(payload)
	if err != nil { t.Fatal(err) }
	return idempotency.Request{Actor: actor, Operation: "test.create", Key: key, RequestHash: hash}
}

func TestIdempotentMutationReplayConflictActorAndRetry(t *testing.T) {
	ctx := context.Background()
	s := openIdemTestStore(t, filepath.Join(t.TempDir(), "store.db"))
	defer s.Close()
	request := idemRequest(t, "actor-a", "same-key", `{"value":"one"}`)
	calls := 0
	mutate := func(tx *sql.Tx) (any, error) {
		calls++
		_, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-a", "one")
		return map[string]any{"saved": true}, err
	}
	first, err := s.runIdempotentMutation(ctx, request, mutate)
	if err != nil || first.Replayed || calls != 1 { t.Fatalf("first=%+v calls=%d err=%v", first, calls, err) }
	second, err := s.runIdempotentMutation(ctx, request, mutate)
	if err != nil || !second.Replayed || second.JSON != first.JSON || calls != 1 { t.Fatalf("replay first=%+v second=%+v calls=%d err=%v", first, second, calls, err) }
	conflict := idemRequest(t, "actor-a", "same-key", `{"value":"different"}`)
	if _, err := s.runIdempotentMutation(ctx, conflict, mutate); !idempotency.IsConflict(err) { t.Fatalf("expected conflict, got %v", err) }
	if calls != 1 { t.Fatalf("conflict ran callback: %d", calls) }
	other := idemRequest(t, "actor-b", "same-key", `{"value":"one"}`)
	if _, err := s.runIdempotentMutation(ctx, other, func(tx *sql.Tx) (any, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-b", "one")
		return map[string]any{"saved": true}, err
	}); err != nil { t.Fatalf("different actor cannot reuse key: %v", err) }
	failure := idemRequest(t, "actor-a", "retryable", `{"value":"later"}`)
	boom := errors.New("precommit failure")
	if _, err := s.runIdempotentMutation(ctx, failure, func(*sql.Tx) (any, error) { return nil, boom }); !errors.Is(err, boom) { t.Fatalf("expected failure, got %v", err) }
	if _, err := s.runIdempotentMutation(ctx, failure, func(tx *sql.Tx) (any, error) {
		_, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-a", "later")
		return map[string]any{"saved": true}, err
	}); err != nil { t.Fatalf("failure was cached: %v", err) }
}

func TestIdempotentMutationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	request := idemRequest(t, "actor-a", "restart-key", `{"value":"persist"}`)
	s := openIdemTestStore(t, path)
	first, err := s.runIdempotentMutation(ctx, request, func(tx *sql.Tx) (any, error) {
		result, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-a", "persist")
		if err != nil { return nil, err }
		id, _ := result.LastInsertId()
		return map[string]any{"id": id}, nil
	})
	if err != nil { t.Fatal(err) }
	if err := s.Close(); err != nil { t.Fatal(err) }
	s = openIdemTestStore(t, path)
	defer s.Close()
	called := false
	replay, err := s.runIdempotentMutation(ctx, request, func(*sql.Tx) (any, error) { called = true; return nil, nil })
	if err != nil || !replay.Replayed || replay.JSON != first.JSON || called { t.Fatalf("restart first=%+v replay=%+v called=%v err=%v", first, replay, called, err) }
}

func TestIdempotentMutationConcurrentEquivalentCommitsOnce(t *testing.T) {
	ctx := context.Background()
	s := openIdemTestStore(t, filepath.Join(t.TempDir(), "concurrent.db"))
	defer s.Close()
	request := idemRequest(t, "actor-a", "concurrent-key", `{"value":"one"}`)
	var callbacks atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.runIdempotentMutation(ctx, request, func(tx *sql.Tx) (any, error) {
				callbacks.Add(1)
				_, err := tx.ExecContext(ctx, `INSERT INTO idem_test_values(actor,value) VALUES(?,?)`, "actor-a", "one")
				return map[string]any{"saved": true}, err
			})
			errs <- err
		}()
	}
	wg.Wait(); close(errs)
	for err := range errs { if err != nil { t.Fatal(err) } }
	if callbacks.Load() != 1 { t.Fatalf("callbacks=%d want=1", callbacks.Load()) }
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM idem_test_values`).Scan(&rows); err != nil { t.Fatal(err) }
	if rows != 1 { t.Fatalf("rows=%d want=1", rows) }
}
