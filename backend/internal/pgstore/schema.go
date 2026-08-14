package pgstore

import (
	"context"
	"fmt"
	"strings"
)

const RequiredSchemaRevision = "20260814070000"

var requiredProductTables = []string{
	"turn_results", "notes", "expenses", "journal_entries", "reminders",
	"conversation_messages", "budgets", "voice_memos", "idempotency_records",
	"legacy_idempotency_reservations", "memories", "memory_vectors", "device_twins",
	"config_overrides", "config_generation", "feature_flags", "entitlements",
	"device_credentials", "outbox", "market_watches", "firmware_releases",
	"llm_usage", "privacy_policies", "feature_modules",
}

var requiredProductTriggers = map[string]string{
	"trg_expenses_ai": "expenses", "trg_expenses_au": "expenses", "trg_expenses_ad": "expenses",
	"trg_budgets_ai": "budgets", "trg_budgets_au": "budgets", "trg_reminders_ai": "reminders",
	"trg_memories_ai": "memories", "trg_twins_au": "device_twins",
}

// VerifySchema fails closed unless Atlas completed the exact schema revision
// compiled into this binary and all atomic-outbox sentinels still exist.
func (s *Store) VerifySchema(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("PostgreSQL store is required")
	}
	var runtimeRole string
	var superuser, createDB, createRole, createSchema bool
	if err := s.pool.QueryRow(ctx, `
		SELECT rolname, rolsuper, rolcreatedb, rolcreaterole,
			has_schema_privilege(rolname, 'public', 'CREATE')
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&runtimeRole, &superuser, &createDB, &createRole, &createSchema); err != nil {
		return fmt.Errorf("verify PostgreSQL runtime role: %w", err)
	}
	if superuser || createDB || createRole || createSchema {
		return fmt.Errorf("PostgreSQL runtime role %q has forbidden DDL/administrative privileges", runtimeRole)
	}
	var applied, total int
	var migrationError string
	var newerRevisions int
	if err := s.pool.QueryRow(ctx, `
		SELECT applied, total, COALESCE(error, ''),
			(SELECT count(*) FROM atlas_schema_revisions.atlas_schema_revisions WHERE version > $1)
		FROM atlas_schema_revisions.atlas_schema_revisions
		WHERE version = $1
	`, RequiredSchemaRevision).Scan(&applied, &total, &migrationError, &newerRevisions); err != nil {
		return fmt.Errorf("verify Atlas schema revision %s: %w", RequiredSchemaRevision, err)
	}
	if applied <= 0 || applied != total || strings.TrimSpace(migrationError) != "" {
		return fmt.Errorf("Atlas schema revision %s is incomplete: applied=%d total=%d error=%q", RequiredSchemaRevision, applied, total, migrationError)
	}
	if newerRevisions != 0 {
		return fmt.Errorf("PostgreSQL schema is newer than companiond revision %s", RequiredSchemaRevision)
	}

	missingTables, err := s.missingSchemaObjects(ctx, `
		SELECT required.name
		FROM unnest($1::text[]) AS required(name)
		WHERE to_regclass(format('public.%I', required.name)) IS NULL
		ORDER BY required.name
	`, requiredProductTables)
	if err != nil {
		return fmt.Errorf("verify PostgreSQL product tables: %w", err)
	}
	if len(missingTables) != 0 {
		return fmt.Errorf("PostgreSQL schema revision %s is missing product tables: %s", RequiredSchemaRevision, strings.Join(missingTables, ", "))
	}

	triggerNames := make([]string, 0, len(requiredProductTriggers))
	triggerRelations := make([]string, 0, len(requiredProductTriggers))
	for name, relation := range requiredProductTriggers {
		triggerNames = append(triggerNames, name)
		triggerRelations = append(triggerRelations, relation)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT required.name
		FROM unnest($1::text[], $2::text[]) AS required(name, relation)
		WHERE NOT EXISTS (
			SELECT 1 FROM pg_trigger
			WHERE tgname = required.name
				AND tgrelid = to_regclass(format('public.%I', required.relation))
				AND NOT tgisinternal
		)
		ORDER BY required.name
	`, triggerNames, triggerRelations)
	if err != nil {
		return fmt.Errorf("verify PostgreSQL outbox triggers: %w", err)
	}
	missingTriggers, err := scanNames(rows)
	if len(missingTriggers) != 0 {
		return fmt.Errorf("PostgreSQL schema revision %s is missing outbox triggers: %s", RequiredSchemaRevision, strings.Join(missingTriggers, ", "))
	}
	return nil
}

func (s *Store) missingSchemaObjects(ctx context.Context, query string, names []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, query, names)
	if err != nil {
		return nil, err
	}
	return scanNames(rows)
}

func scanNames(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) ([]string, error) {
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		missing = append(missing, name)
	}
	return missing, rows.Err()
}
