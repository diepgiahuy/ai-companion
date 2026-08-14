// Command companion-migrate is an explicit offline cutover tool. It is never
// invoked by companiond and therefore cannot create a runtime SQLite/PostgreSQL
// selector or dual-write path.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"companion-server/internal/pgstore"
	pgmigrate "companion-server/internal/pgstore/migrate"
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
		return errors.New("usage: companion-migrate import|verify|digest-postgres [flags]")
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
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func openBoth(ctx context.Context, sqlitePath, postgresDSN string) (*sql.DB, *pgxpool.Pool, func(), error) {
	sqlitePath = strings.TrimSpace(sqlitePath)
	if sqlitePath == "" { return nil, nil, nil, errors.New("--sqlite is required") }
	source, err := sql.Open("sqlite", sqlitePath)
	if err != nil { return nil, nil, nil, fmt.Errorf("open SQLite: %w", err) }
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
