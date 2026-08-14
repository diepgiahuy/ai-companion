package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"companion-server/internal/idempotency"
)

func TestLegacyGlobalIdempotencyMigratesToReservations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',content TEXT NOT NULL,created_at TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO notes(idempotency_key,user_id,content,created_at) VALUES('legacy-note','legacy-user','old','2030-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE expenses (id INTEGER PRIMARY KEY AUTOINCREMENT,idempotency_key TEXT NOT NULL UNIQUE,user_id TEXT NOT NULL DEFAULT '',amount_vnd INTEGER NOT NULL CHECK(amount_vnd>0),category TEXT NOT NULL,description TEXT NOT NULL,occurred_at TEXT NOT NULL,created_at TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO expenses(idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES('legacy-batch:0','legacy-user',10000,'food','old','2030-01-01T00:00:00Z','2030-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"notes", "expenses"} {
		unique, err := hasSingleColumnUniqueIndex(s.db, table, "idempotency_key")
		if err != nil {
			t.Fatal(err)
		}
		if unique {
			t.Fatalf("%s still has global idempotency uniqueness", table)
		}
	}
	notes, err := s.ListNotes(context.Background(), "legacy-user", 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "old" {
		t.Fatalf("legacy note not preserved: %+v %v", notes, err)
	}

	hash, _ := idempotency.HashValue(map[string]any{"content": "new"})
	err = s.CreateNoteMutation(context.Background(), idempotency.Request{Actor: "actor:new", Operation: "note.create", Key: "legacy-note", RequestHash: hash}, "new-user", "new")
	if !idempotency.IsConflict(err) {
		t.Fatalf("legacy note key should be reserved, got %v", err)
	}
	batchHash, _ := idempotency.HashValue([]any{map[string]any{"amount_vnd": 10000}})
	err = s.CreateExpensesMutation(context.Background(), idempotency.Request{Actor: "actor:new", Operation: "expense.log", Key: "legacy-batch", RequestHash: batchHash}, "new-user", []ExpenseInput{{AmountVND: 10000, Category: "food", OccurredAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}})
	if !idempotency.IsConflict(err) {
		t.Fatalf("legacy batch parent should be reserved, got %v", err)
	}
}

func TestActorScopedKeysSurviveRestartAndConcurrentRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actor.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := idempotency.HashValue(map[string]any{"content": "same"})
	for _, actor := range []struct{ actor, user string }{{"actor:a", "user-a"}, {"actor:b", "user-b"}} {
		req := idempotency.Request{Actor: actor.actor, Operation: "note.create", Key: "shared-key", RequestHash: hash}
		if err := s.CreateNoteMutation(context.Background(), req, actor.user, "same"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	req := idempotency.Request{Actor: "actor:a", Operation: "note.create", Key: "shared-key", RequestHash: hash}
	if err := s.CreateNoteMutation(context.Background(), req, "user-a", "same"); err != nil {
		t.Fatalf("restart replay failed: %v", err)
	}

	concurrentHash, _ := idempotency.HashValue(map[string]any{"content": "concurrent"})
	concurrent := idempotency.Request{Actor: "actor:c", Operation: "note.create", Key: "concurrent-key", RequestHash: concurrentHash}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.CreateNoteMutation(context.Background(), concurrent, "user-c", "concurrent")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent replay: %v", err)
		}
	}
	notes, err := s.ListNotes(context.Background(), "user-c", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("concurrent mutation count=%d, want 1", len(notes))
	}
}

func TestFreshSchemaAllowsActorScopedMarketWatchKey(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "market.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	unique, err := hasSingleColumnUniqueIndex(s.db, "market_watches", "idempotency_key")
	if err != nil {
		t.Fatal(err)
	}
	if unique {
		t.Fatal("fresh market_watches still has global unique key")
	}
	hash, _ := idempotency.HashValue(map[string]any{"symbol": "XAU/USD"})
	for _, x := range []struct{ actor, user string }{{"actor:a", "user-a"}, {"actor:b", "user-b"}} {
		_, err := s.CreateMarketWatchMutation(context.Background(), idempotency.Request{Actor: x.actor, Operation: "market.watch.create", Key: "watch-key", RequestHash: hash}, x.user, "device", "provider", "XAU/USD", "USD", ">", 3000)
		if err != nil {
			t.Fatal(err)
		}
	}
}
