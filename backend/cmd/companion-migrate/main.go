// Command companion-migrate is an explicit offline cutover/recovery tool. It is
// never invoked by companiond and therefore cannot create a runtime
// SQLite/PostgreSQL selector or dual-write path.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"companion-server/internal/pgstore"
	pgmigrate "companion-server/internal/pgstore/migrate"
	sqlitestore "companion-server/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "companion-migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: companion-migrate import|verify|digest-postgres|export-sqlite [flags]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch args[0] {
	case "import":
		fs := flag.NewFlagSet("import", flag.ContinueOnError)
		sqlitePath := fs.String("sqlite", "", "source SQLite database path")
		postgresDSN := fs.String("postgres", "", "fresh Atlas-migrated PostgreSQL DSN")
		if err := fs.Parse(args[1:]); err != nil { return err }
		source, target, closeAll, err := openBoth(ctx, *sqlitePath, *postgresDSN)
		if err != nil { return err }
		defer closeAll()
		report, err := pgmigrate.ImportSQLite(ctx, source, target)
		if err != nil { return err }
		return writeReport(report)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ContinueOnError)
		sqlitePath := fs.String("sqlite", "", "source SQLite database path")
		postgresDSN := fs.String("postgres", "", "PostgreSQL DSN")
		if err := fs.Parse(args[1:]); err != nil { return err }
		source, target, closeAll, err := openBoth(ctx, *sqlitePath, *postgresDSN)
		if err != nil { return err }
		defer closeAll()
		report, err := pgmigrate.VerifyParity(ctx, source, target)
		if err != nil { return err }
		return writeReport(report)
	case "digest-postgres":
		fs := flag.NewFlagSet("digest-postgres", flag.ContinueOnError)
		postgresDSN := fs.String("postgres", "", "PostgreSQL DSN")
		if err := fs.Parse(args[1:]); err != nil { return err }
		pool, err := openPostgres(ctx, *postgresDSN)
		if err != nil { return err }
		defer pool.Close()
		report, err := pgmigrate.DigestPostgres(ctx, pool)
		if err != nil { return err }
		return writeReport(report)
	case "export-sqlite":
		fs := flag.NewFlagSet("export-sqlite", flag.ContinueOnError)
		postgresDSN := fs.String("postgres", "", "authoritative PostgreSQL DSN")
		sqlitePath := fs.String("sqlite", "", "new recovery SQLite database path")
		if err := fs.Parse(args[1:]); err != nil { return err }
		report, err := exportSQLite(ctx, *postgresDSN, *sqlitePath)
		if err != nil { return err }
		return writeReport(report)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func exportSQLite(ctx context.Context, postgresDSN, destination string) (pgmigrate.Report, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return pgmigrate.Report{}, errors.New("--sqlite is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return pgmigrate.Report{}, fmt.Errorf("recovery SQLite destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return pgmigrate.Report{}, fmt.Errorf("inspect recovery SQLite destination: %w", err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return pgmigrate.Report{}, fmt.Errorf("prepare recovery directory: %w", err)
	}
	tempFile, err := os.CreateTemp(parent, "."+filepath.Base(destination)+".recovery-*.sqlite")
	if err != nil {
		return pgmigrate.Report{}, fmt.Errorf("create recovery SQLite temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return pgmigrate.Report{}, err
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.Remove(tempPath)
			_ = os.Remove(tempPath + "-wal")
			_ = os.Remove(tempPath + "-shm")
		}
	}()

	// Initialize only the current SQLite schema. This is explicit migration
	// tooling; product startup is never pointed at this path automatically.
	initialized, err := sqlitestore.Open(tempPath)
	if err != nil {
		return pgmigrate.Report{}, fmt.Errorf("initialize recovery SQLite schema: %w", err)
	}
	if err := initialized.Close(); err != nil {
		return pgmigrate.Report{}, fmt.Errorf("close initialized recovery SQLite: %w", err)
	}
	target, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return pgmigrate.Report{}, fmt.Errorf("open recovery SQLite: %w", err)
	}
	target.SetMaxOpenConns(1)
	pool, err := openPostgres(ctx, postgresDSN)
	if err != nil {
		_ = target.Close()
		return pgmigrate.Report{}, err
	}
	report, exportErr := pgmigrate.ExportPostgresToSQLite(ctx, pool, target)
	pool.Close()
	if exportErr != nil {
		_ = target.Close()
		return pgmigrate.Report{}, exportErr
	}
	// Consolidate WAL state so the promoted rollback artifact is a standalone
	// SQLite file and can be moved/restored without sidecar files.
	if _, err := target.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = target.Close()
		return pgmigrate.Report{}, fmt.Errorf("checkpoint recovery SQLite WAL: %w", err)
	}
	if _, err := target.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
		_ = target.Close()
		return pgmigrate.Report{}, fmt.Errorf("finalize recovery SQLite journal: %w", err)
	}
	if err := target.Close(); err != nil {
		return pgmigrate.Report{}, fmt.Errorf("close recovery SQLite: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return pgmigrate.Report{}, fmt.Errorf("promote recovery SQLite artifact: %w", err)
	}
	promoted = true
	return report, nil
}

func openBoth(ctx context.Context, sqlitePath, postgresDSN string) (*sql.DB, *pgxpool.Pool, func(), error) {
	sqlitePath = strings.TrimSpace(sqlitePath)
	if sqlitePath == "" { return nil, nil, nil, errors.New("--sqlite is required") }
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil { return nil, nil, nil, fmt.Errorf("open SQLite: %w", err) }
	source.SetMaxOpenConns(1)
	if err := source.PingContext(ctx); err != nil {
		source.Close()
		return nil, nil, nil, fmt.Errorf("ping SQLite: %w", err)
	}
	target, err := openPostgres(ctx, postgresDSN)
	if err != nil {
		source.Close()
		return nil, nil, nil, err
	}
	closeAll := func() { target.Close(); _ = source.Close() }
	return source, target, closeAll, nil
}

func openPostgres(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" { return nil, errors.New("--postgres is required") }
	return pgstore.Open(ctx, pgstore.PoolConfig{DSN: dsn, MaxConns: 4})
}

func writeReport(report pgmigrate.Report) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
