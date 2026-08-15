// Command companion-river-migrate explicitly manages River-owned PostgreSQL schema.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"companion-server/internal/jobs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type report struct {
	Action     string `json:"action"`
	Schema     string `json:"schema"`
	Direction  string `json:"direction,omitempty"`
	Versions   []int  `json:"versions,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Valid      bool   `json:"valid"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "companion-river-migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("action is required: up or validate")
	}
	action := args[0]
	flags := flag.NewFlagSet("companion-river-migrate "+action, flag.ContinueOnError)
	databaseURL := flags.String("database-url", "", "migration/admin PostgreSQL URL")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	dsn := strings.TrimSpace(*databaseURL)
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("COMPANION_RIVER_MIGRATION_DATABASE_URL"))
	}
	if dsn == "" {
		return errors.New("--database-url or COMPANION_RIVER_MIGRATION_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: jobs.Schema})
	if err != nil {
		return err
	}
	started := time.Now()
	result := report{Action: action, Schema: jobs.Schema}
	switch action {
	case "up":
		if err := ensureMigrationSchema(ctx, pool); err != nil {
			return err
		}
		migrated, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
		if err != nil {
			return err
		}
		result.Direction = string(migrated.Direction)
		for _, version := range migrated.Versions {
			result.Versions = append(result.Versions, version.Version)
		}
	case "validate":
		validation, err := migrator.Validate(ctx, nil)
		if err != nil {
			return err
		}
		if !validation.OK {
			return fmt.Errorf("River schema validation failed: %s", strings.Join(validation.Messages, "; "))
		}
	default:
		return fmt.Errorf("unsupported action %q: use up or validate", action)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Valid = true
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func ensureMigrationSchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin River schema bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS river`); err != nil {
		return fmt.Errorf("create River schema: %w", err)
	}
	var currentUser, owner string
	if err := tx.QueryRow(ctx, `
		SELECT current_user, pg_get_userbyid(nspowner)
		FROM pg_namespace
		WHERE nspname = $1`, jobs.Schema).Scan(&currentUser, &owner); err != nil {
		return fmt.Errorf("inspect River schema owner: %w", err)
	}
	if owner != currentUser {
		return fmt.Errorf("River schema owner is %q, migration role is %q", owner, currentUser)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit River schema bootstrap: %w", err)
	}
	return nil
}
