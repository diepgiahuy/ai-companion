package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type sqliteTrigger struct {
	name string
	ddl  string
}

// ExportPostgresToSQLite is an explicit offline disaster-recovery path. The
// caller must supply a fresh SQLite database already initialized with the
// current Companion schema. It is intentionally not used by companiond and
// must never become a runtime fallback or dual-write path.
func ExportPostgresToSQLite(ctx context.Context, source *pgxpool.Pool, target *sql.DB) (Report, error) {
	if source == nil || target == nil {
		return Report{}, errors.New("source PostgreSQL and target SQLite are required")
	}
	if err := checkPostgresShape(ctx, source); err != nil {
		return Report{}, err
	}
	target.SetMaxOpenConns(1)
	if _, err := target.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return Report{}, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	if err := checkSQLiteShape(ctx, target); err != nil {
		return Report{}, err
	}

	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, fmt.Errorf("begin SQLite recovery export: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireFreshSQLiteTarget(ctx, tx); err != nil {
		return Report{}, err
	}
	triggers, err := loadSQLiteTriggers(ctx, tx)
	if err != nil {
		return Report{}, err
	}
	// Domain rows copied from PostgreSQL already have their durable outbox state.
	// Disable every current-schema SQLite trigger transactionally while copying so
	// recovery does not synthesize duplicate events. Restore the exact DDL before
	// the recovered database can be promoted.
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+quoteIdent(trigger.name)); err != nil {
			return Report{}, fmt.Errorf("drop SQLite trigger %s: %w", trigger.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM config_generation`); err != nil {
		return Report{}, fmt.Errorf("clear SQLite config_generation baseline: %w", err)
	}
	for _, table := range tables {
		if err := exportTableToSQLite(ctx, source, tx, table); err != nil {
			return Report{}, err
		}
	}
	for _, trigger := range triggers {
		if _, err := tx.ExecContext(ctx, trigger.ddl); err != nil {
			return Report{}, fmt.Errorf("restore SQLite trigger %s: %w", trigger.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Report{}, fmt.Errorf("commit SQLite recovery export: %w", err)
	}
	return VerifyParity(ctx, target, source)
}

func requireFreshSQLiteTarget(ctx context.Context, tx *sql.Tx) error {
	for _, table := range tables {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdent(table.name)).Scan(&count); err != nil {
			return fmt.Errorf("count SQLite %s: %w", table.name, err)
		}
		if table.name == "config_generation" {
			if count != 1 {
				return fmt.Errorf("SQLite target config_generation must contain only current-schema baseline row, got %d rows", count)
			}
			var id, version int64
			if err := tx.QueryRowContext(ctx, `SELECT id,version FROM config_generation`).Scan(&id, &version); err != nil {
				return err
			}
			if id != 1 || version != 1 {
				return fmt.Errorf("SQLite target config_generation baseline is not 1:1")
			}
			continue
		}
		if count != 0 {
			return fmt.Errorf("target SQLite is not fresh: %s has %d rows", table.name, count)
		}
	}
	return nil
}

func loadSQLiteTriggers(ctx context.Context, tx *sql.Tx) ([]sqliteTrigger, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_master WHERE type='trigger' AND sql IS NOT NULL ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read SQLite triggers: %w", err)
	}
	defer rows.Close()
	var out []sqliteTrigger
	for rows.Next() {
		var trigger sqliteTrigger
		if err := rows.Scan(&trigger.name, &trigger.ddl); err != nil {
			return nil, err
		}
		if strings.TrimSpace(trigger.name) == "" || strings.TrimSpace(trigger.ddl) == "" {
			return nil, fmt.Errorf("SQLite trigger metadata is incomplete")
		}
		out = append(out, trigger)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func exportTableToSQLite(ctx context.Context, source *pgxpool.Pool, tx *sql.Tx, table tableSpec) error {
	rows, err := source.Query(ctx, fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", columnList(table), quoteIdent(table.name), table.orderBy))
	if err != nil {
		return fmt.Errorf("read PostgreSQL %s: %w", table.name, err)
	}
	defer rows.Close()
	placeholders := make([]string, len(table.columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s)", quoteIdent(table.name), columnList(table), strings.Join(placeholders, ","))
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return fmt.Errorf("scan PostgreSQL %s: %w", table.name, err)
		}
		for i, column := range table.columns {
			values[i], err = sqliteValue(column.kind, values[i])
			if err != nil {
				return fmt.Errorf("normalize %s.%s for SQLite recovery: %w", table.name, column.name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, insertSQL, values...); err != nil {
			return fmt.Errorf("insert SQLite %s: %w", table.name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate PostgreSQL %s: %w", table.name, err)
	}
	return nil
}

func sqliteValue(kind valueKind, value any) (any, error) {
	if value == nil {
		if kind == nullableTimeValue {
			return "", nil
		}
		return nil, nil
	}
	switch kind {
	case boolValue:
		normalized, err := canonicalValue(kind, value)
		if err != nil {
			return nil, err
		}
		if normalized.(bool) {
			return int64(1), nil
		}
		return int64(0), nil
	case timeValue, nullableTimeValue:
		normalized, err := canonicalValue(kind, value)
		if err != nil {
			return nil, err
		}
		if normalized == nil {
			return "", nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, normalized.(string))
		if err != nil {
			return nil, err
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	case jsonValue:
		normalized, err := canonicalValue(kind, value)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		return string(encoded), nil
	default:
		if raw, ok := value.([]byte); ok {
			return string(raw), nil
		}
		return value, nil
	}
}
