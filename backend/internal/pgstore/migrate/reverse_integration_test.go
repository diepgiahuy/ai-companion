package migrate_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"companion-server/internal/pgstore"
	pgmigrate "companion-server/internal/pgstore/migrate"
	"companion-server/internal/store"
	_ "modernc.org/sqlite"
)

func TestPostgresToSQLiteRecoveryParityAndRuntimeShape(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_POSTGRES_MIGRATION_TEST_DSN"))
	if dsn == "" {
		t.Skip("COMPANION_POSTGRES_MIGRATION_TEST_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgstore.Open(ctx, pgstore.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	resetPostgres(t, ctx, pool)

	sourcePath := filepath.Join(t.TempDir(), "source.sqlite")
	initializedSource, err := store.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := initializedSource.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	source.SetMaxOpenConns(1)
	seedSQLite(t, source)
	forward, err := pgmigrate.ImportSQLite(ctx, source, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	recoveryPath := filepath.Join(t.TempDir(), "recovered.sqlite")
	initializedRecovery, err := store.Open(recoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := initializedRecovery.Close(); err != nil {
		t.Fatal(err)
	}
	recovery, err := sql.Open("sqlite", recoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	recovery.SetMaxOpenConns(1)
	reverse, err := pgmigrate.ExportPostgresToSQLite(ctx, pool, recovery)
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse.Tables) != 25 {
		t.Fatalf("reverse table coverage=%d want=25", len(reverse.Tables))
	}
	if !reflect.DeepEqual(forward.Tables, reverse.Tables) {
		t.Fatalf("reverse recovery digest differs from authoritative PostgreSQL\nforward=%+v\nreverse=%+v", forward.Tables, reverse.Tables)
	}
	if _, err := pgmigrate.ExportPostgresToSQLite(ctx, pool, recovery); err == nil || !strings.Contains(err.Error(), "not fresh") {
		t.Fatalf("second recovery export must fail closed on non-fresh SQLite target, got %v", err)
	}
	if err := recovery.Close(); err != nil {
		t.Fatal(err)
	}

	// The exported artifact must remain a normal current SQLite Store rather than
	// a digest-only dump. Reopen it through the real SQLite implementation and
	// prove both domain reads and current outbox triggers still work.
	recoveredStore, err := store.Open(recoveryPath)
	if err != nil {
		t.Fatalf("reopen recovery artifact through SQLite Store: %v", err)
	}
	defer recoveredStore.Close()
	notes, err := recoveredStore.ListNotes(ctx, "u1", 10)
	if err != nil || len(notes) != 1 || notes[0].Content != "xin chào" {
		t.Fatalf("recovered SQLite application read notes=%+v err=%v", notes, err)
	}
	inspector, err := sql.Open("sqlite", recoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	var before int
	if err := inspector.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := recoveredStore.CreateExpense(ctx, "u1", "rollback-trigger-check", 12345, "test", "trigger survives recovery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := inspector.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("recovered SQLite outbox trigger inactive: before=%d after=%d", before, after)
	}
}
