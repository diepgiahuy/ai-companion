package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type valueKind uint8

const (
	plainValue valueKind = iota
	timeValue
	nullableTimeValue
	boolValue
	jsonValue
)

type columnSpec struct {
	name string
	kind valueKind
}

type tableSpec struct {
	name       string
	columns    []columnSpec
	orderBy    string
	identityID string
}

func c(name string, kind valueKind) columnSpec { return columnSpec{name: name, kind: kind} }
func plain(names ...string) []columnSpec {
	out := make([]columnSpec, 0, len(names))
	for _, name := range names {
		out = append(out, c(name, plainValue))
	}
	return out
}
func cols(parts ...[]columnSpec) []columnSpec {
	var out []columnSpec
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

var tables = []tableSpec{
	{name: "turn_results", columns: cols(plain("turn_id", "response"), []columnSpec{c("created_at", timeValue)}), orderBy: "turn_id"},
	{name: "notes", columns: cols(plain("id", "idempotency_key", "user_id", "content"), []columnSpec{c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "expenses", columns: cols(plain("id", "idempotency_key", "user_id", "amount_vnd", "category", "description"), []columnSpec{c("occurred_at", timeValue), c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "journal_entries", columns: cols(plain("id", "idempotency_key", "user_id", "content"), []columnSpec{c("occurred_at", timeValue), c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "reminders", columns: cols(plain("id", "idempotency_key", "user_id", "device_id", "kind", "title"), []columnSpec{c("fire_at", timeValue)}, plain("status", "attempts"), []columnSpec{c("next_attempt_at", nullableTimeValue)}, plain("paused_remaining_seconds"), []columnSpec{c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "conversation_messages", columns: cols(plain("id", "turn_key", "user_id", "thread_id", "role", "content"), []columnSpec{c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "budgets", columns: cols(plain("user_id", "period", "limit_vnd"), []columnSpec{c("updated_at", timeValue)}), orderBy: "user_id,period"},
	{name: "voice_memos", columns: cols(plain("id", "idempotency_key", "user_id", "device_id", "path", "transcript", "duration_ms"), []columnSpec{c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "voice_mail_items", columns: cols(plain("id", "sender_user_id", "sender_device_id", "recipient_user_id", "recipient_device_id", "object_key", "media_format", "duration_ms", "size_bytes", "checksum_sha256", "policy", "state", "playback_id"), []columnSpec{c("lease_expires_at", nullableTimeValue), c("expires_at", timeValue), c("created_at", timeValue), c("updated_at", timeValue)}), orderBy: "id"},
	{name: "idempotency_records", columns: cols(plain("actor_id", "operation", "idempotency_key", "request_hash"), []columnSpec{c("outcome_json", jsonValue), c("created_at", timeValue)}), orderBy: "actor_id,operation,idempotency_key"},
	{name: "legacy_idempotency_reservations", columns: cols(plain("operation", "idempotency_key", "source_table"), []columnSpec{c("created_at", timeValue)}), orderBy: "operation,idempotency_key"},
	{name: "memories", columns: cols(plain("id", "user_id", "memory_key", "kind", "value"), []columnSpec{c("valid_from", timeValue), c("valid_to", nullableTimeValue)}, plain("source", "confidence"), []columnSpec{c("embedding", jsonValue), c("created_at", timeValue), c("deleted_at", nullableTimeValue)}), orderBy: "id", identityID: "id"},
	{name: "memory_vectors", columns: cols(plain("memory_id", "user_id"), []columnSpec{c("embedding", jsonValue), c("updated_at", timeValue)}), orderBy: "memory_id"},
	{name: "device_twins", columns: cols(plain("device_id", "user_id"), []columnSpec{c("desired_json", jsonValue)}, plain("desired_version"), []columnSpec{c("reported_json", jsonValue)}, plain("reported_version"), []columnSpec{c("updated_at", timeValue)}), orderBy: "device_id"},
	{name: "config_overrides", columns: cols(plain("scope_type", "scope_id"), []columnSpec{c("config_json", jsonValue)}, plain("version"), []columnSpec{c("updated_at", timeValue)}), orderBy: "scope_type,scope_id"},
	{name: "config_generation", columns: plain("id", "version"), orderBy: "id"},
	{name: "feature_flags", columns: cols(plain("key"), []columnSpec{c("enabled", boolValue)}, plain("rollout", "required_plan"), []columnSpec{c("variants_json", jsonValue)}, plain("lifecycle", "owner"), []columnSpec{c("expires_at", nullableTimeValue), c("updated_at", timeValue)}), orderBy: "key"},
	{name: "entitlements", columns: cols(plain("subject_type", "subject_id", "entitlement"), []columnSpec{c("enabled", boolValue), c("expires_at", nullableTimeValue), c("updated_at", timeValue)}), orderBy: "subject_type,subject_id,entitlement"},
	{name: "device_credentials", columns: cols(plain("device_id", "user_id", "tenant_id", "plan", "token_sha256", "status"), []columnSpec{c("created_at", timeValue), c("rotated_at", timeValue)}), orderBy: "device_id"},
	{name: "outbox", columns: cols(plain("id", "event_id", "source", "event_type", "subject", "user_id"), []columnSpec{c("data_json", jsonValue), c("occurred_at", timeValue)}, plain("status", "attempts"), []columnSpec{c("next_attempt_at", timeValue)}, plain("last_error")), orderBy: "id", identityID: "id"},
	{name: "market_watches", columns: cols(plain("id", "idempotency_key", "user_id", "device_id", "provider", "symbol", "currency", "operator", "threshold"), []columnSpec{c("enabled", boolValue), c("last_state", boolValue), c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "firmware_releases", columns: cols(plain("metadata_version", "version", "channel", "board", "protocol_min", "security_version", "url", "sha256", "size"), []columnSpec{c("expires_at", timeValue)}, plain("signature"), []columnSpec{c("manifest_json", jsonValue), c("created_at", timeValue)}), orderBy: "metadata_version"},
	{name: "llm_usage", columns: cols(plain("id", "user_id", "device_id", "provider", "model", "prompt_version", "prompt_tokens", "completion_tokens", "total_tokens"), []columnSpec{c("created_at", timeValue)}), orderBy: "id", identityID: "id"},
	{name: "privacy_policies", columns: cols(plain("user_id"), []columnSpec{c("save_voice_audio", boolValue)}, plain("voice_mail_policy"), []columnSpec{c("long_term_memory_enabled", boolValue)}, plain("conversation_retention_days", "voice_memo_retention_days", "memory_retention_days"), []columnSpec{c("updated_at", timeValue)}), orderBy: "user_id"},
	{name: "feature_modules", columns: cols(plain("id", "version", "lifecycle", "execution"), []columnSpec{c("manifest_json", jsonValue), c("updated_at", timeValue)}), orderBy: "id"},
}

var outboxTriggerTables = []string{"expenses", "budgets", "reminders", "memories", "device_twins"}

type TableDigest struct {
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

type Report struct {
	Tables map[string]TableDigest `json:"tables"`
}

func ImportSQLite(ctx context.Context, source *sql.DB, target *pgxpool.Pool) (Report, error) {
	if source == nil || target == nil {
		return Report{}, errors.New("source SQLite and target PostgreSQL are required")
	}
	if err := checkSQLiteShape(ctx, source); err != nil {
		return Report{}, err
	}
	if err := checkPostgresShape(ctx, target); err != nil {
		return Report{}, err
	}

	tx, err := target.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, fmt.Errorf("begin PostgreSQL import: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := requireFreshTarget(ctx, tx); err != nil {
		return Report{}, err
	}
	for _, table := range outboxTriggerTables {
		if _, err := tx.Exec(ctx, "ALTER TABLE "+quoteIdent(table)+" DISABLE TRIGGER USER"); err != nil {
			return Report{}, fmt.Errorf("disable %s user triggers: %w", table, err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM config_generation`); err != nil {
		return Report{}, fmt.Errorf("clear config_generation baseline: %w", err)
	}

	for _, table := range tables {
		if err := importTable(ctx, source, tx, table); err != nil {
			return Report{}, err
		}
	}
	for _, table := range outboxTriggerTables {
		if _, err := tx.Exec(ctx, "ALTER TABLE "+quoteIdent(table)+" ENABLE TRIGGER USER"); err != nil {
			return Report{}, fmt.Errorf("enable %s user triggers: %w", table, err)
		}
	}
	for _, table := range tables {
		if table.identityID == "" {
			continue
		}
		query := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','%s'), COALESCE(MAX(%s),1), MAX(%s) IS NOT NULL) FROM %s`, table.name, table.identityID, quoteIdent(table.identityID), quoteIdent(table.identityID), quoteIdent(table.name))
		if _, err := tx.Exec(ctx, query); err != nil {
			return Report{}, fmt.Errorf("reset %s identity sequence: %w", table.name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit PostgreSQL import: %w", err)
	}
	return VerifyParity(ctx, source, target)
}

func VerifyParity(ctx context.Context, source *sql.DB, target *pgxpool.Pool) (Report, error) {
	sourceDigest, err := DigestSQLite(ctx, source)
	if err != nil {
		return Report{}, err
	}
	targetDigest, err := DigestPostgres(ctx, target)
	if err != nil {
		return Report{}, err
	}
	for _, table := range tables {
		left, right := sourceDigest.Tables[table.name], targetDigest.Tables[table.name]
		if left.Rows != right.Rows || left.SHA256 != right.SHA256 {
			return Report{}, fmt.Errorf("parity mismatch table=%s sqlite(rows=%d sha=%s) postgres(rows=%d sha=%s)", table.name, left.Rows, left.SHA256, right.Rows, right.SHA256)
		}
	}
	return targetDigest, nil
}

func DigestSQLite(ctx context.Context, db *sql.DB) (Report, error) {
	report := Report{Tables: make(map[string]TableDigest, len(tables))}
	for _, table := range tables {
		digest, err := digestSQLiteTable(ctx, db, table)
		if err != nil {
			return Report{}, err
		}
		report.Tables[table.name] = digest
	}
	return report, nil
}

func DigestPostgres(ctx context.Context, pool *pgxpool.Pool) (Report, error) {
	report := Report{Tables: make(map[string]TableDigest, len(tables))}
	for _, table := range tables {
		digest, err := digestPostgresTable(ctx, pool, table)
		if err != nil {
			return Report{}, err
		}
		report.Tables[table.name] = digest
	}
	return report, nil
}

func importTable(ctx context.Context, source *sql.DB, tx pgx.Tx, table tableSpec) error {
	columnNames := make([]string, len(table.columns))
	placeholders := make([]string, len(table.columns))
	for i, column := range table.columns {
		columnNames[i] = quoteIdent(column.name)
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	selectSQL := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", strings.Join(columnNames, ","), quoteIdent(table.name), table.orderBy)
	rows, err := source.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("read SQLite %s: %w", table.name, err)
	}
	defer rows.Close()
	insertSQL := fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s)", quoteIdent(table.name), strings.Join(columnNames, ","), strings.Join(placeholders, ","))
	for rows.Next() {
		values := make([]any, len(table.columns))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("scan SQLite %s: %w", table.name, err)
		}
		for i, column := range table.columns {
			values[i], err = postgresValue(column.kind, values[i])
			if err != nil {
				return fmt.Errorf("normalize %s.%s for import: %w", table.name, column.name, err)
			}
		}
		if _, err := tx.Exec(ctx, insertSQL, values...); err != nil {
			return fmt.Errorf("insert PostgreSQL %s: %w", table.name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite %s: %w", table.name, err)
	}
	return nil
}

func requireFreshTarget(ctx context.Context, tx pgx.Tx) error {
	for _, table := range tables {
		var count int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+quoteIdent(table.name)).Scan(&count); err != nil {
			return fmt.Errorf("count PostgreSQL %s: %w", table.name, err)
		}
		if table.name == "config_generation" {
			if count != 1 {
				return fmt.Errorf("target config_generation must contain only Atlas baseline row, got %d rows", count)
			}
			var id, version int64
			if err := tx.QueryRow(ctx, `SELECT id,version FROM config_generation`).Scan(&id, &version); err != nil {
				return err
			}
			if id != 1 || version != 1 {
				return fmt.Errorf("target config_generation baseline is not 1:1")
			}
			continue
		}
		if count != 0 {
			return fmt.Errorf("target PostgreSQL is not fresh: %s has %d rows", table.name, count)
		}
	}
	return nil
}

func checkSQLiteShape(ctx context.Context, db *sql.DB) error {
	for _, table := range tables {
		rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdent(table.name)+")")
		if err != nil {
			return fmt.Errorf("inspect SQLite %s: %w", table.name, err)
		}
		seen := map[string]bool{}
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			seen[name] = true
		}
		rows.Close()
		for _, column := range table.columns {
			if !seen[column.name] {
				return fmt.Errorf("SQLite schema missing %s.%s", table.name, column.name)
			}
		}
	}
	return nil
}

func checkPostgresShape(ctx context.Context, pool *pgxpool.Pool) error {
	for _, table := range tables {
		rows, err := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table.name)
		if err != nil {
			return fmt.Errorf("inspect PostgreSQL %s: %w", table.name, err)
		}
		seen := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			seen[name] = true
		}
		rows.Close()
		for _, column := range table.columns {
			if !seen[column.name] {
				return fmt.Errorf("PostgreSQL schema missing %s.%s", table.name, column.name)
			}
		}
	}
	return nil
}

func digestSQLiteTable(ctx context.Context, db *sql.DB, table tableSpec) (TableDigest, error) {
	columns := columnList(table)
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", columns, quoteIdent(table.name), table.orderBy))
	if err != nil {
		return TableDigest{}, fmt.Errorf("digest SQLite %s: %w", table.name, err)
	}
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		values := make([]any, len(table.columns))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return TableDigest{}, err
		}
		canonical, err := canonicalRow(table, values)
		if err != nil {
			return TableDigest{}, err
		}
		h.Write(canonical)
		h.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		return TableDigest{}, err
	}
	return TableDigest{Rows: count, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func digestPostgresTable(ctx context.Context, pool *pgxpool.Pool, table tableSpec) (TableDigest, error) {
	rows, err := pool.Query(ctx, fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", columnList(table), quoteIdent(table.name), table.orderBy))
	if err != nil {
		return TableDigest{}, fmt.Errorf("digest PostgreSQL %s: %w", table.name, err)
	}
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return TableDigest{}, err
		}
		canonical, err := canonicalRow(table, values)
		if err != nil {
			return TableDigest{}, err
		}
		h.Write(canonical)
		h.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		return TableDigest{}, err
	}
	return TableDigest{Rows: count, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func canonicalRow(table tableSpec, values []any) ([]byte, error) {
	if len(values) != len(table.columns) {
		return nil, fmt.Errorf("%s row has %d values, want %d", table.name, len(values), len(table.columns))
	}
	out := make([]any, len(values))
	for i, column := range table.columns {
		normalized, err := canonicalValue(column.kind, values[i])
		if err != nil {
			return nil, fmt.Errorf("canonicalize %s.%s: %w", table.name, column.name, err)
		}
		out[i] = normalized
	}
	return json.Marshal(out)
}

func canonicalValue(kind valueKind, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch kind {
	case boolValue:
		switch v := value.(type) {
		case bool:
			return v, nil
		case int64:
			return v != 0, nil
		case int32:
			return v != 0, nil
		case int:
			return v != 0, nil
		case []byte:
			return parseBool(string(v))
		case string:
			return parseBool(v)
		}
		return nil, fmt.Errorf("unsupported boolean %T", value)
	case timeValue, nullableTimeValue:
		if t, ok := value.(time.Time); ok {
			return t.UTC().Format(time.RFC3339Nano), nil
		}
		raw := strings.TrimSpace(stringValue(value))
		if raw == "" && kind == nullableTimeValue {
			return nil, nil
		}
		t, err := parseTime(raw)
		if err != nil {
			return nil, err
		}
		return t.UTC().Format(time.RFC3339Nano), nil
	case jsonValue:
		var decoded any
		switch v := value.(type) {
		case []byte:
			if err := json.Unmarshal(v, &decoded); err != nil {
				return nil, err
			}
		case string:
			if err := json.Unmarshal([]byte(v), &decoded); err != nil {
				return nil, err
			}
		default:
			decoded = v
		}
		return decoded, nil
	default:
		if b, ok := value.([]byte); ok {
			return string(b), nil
		}
		return value, nil
	}
}

func postgresValue(kind valueKind, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch kind {
	case boolValue:
		return canonicalValue(kind, value)
	case timeValue, nullableTimeValue:
		normalized, err := canonicalValue(kind, value)
		if err != nil || normalized == nil {
			return normalized, err
		}
		return time.Parse(time.RFC3339Nano, normalized.(string))
	case jsonValue:
		normalized, err := canonicalValue(kind, value)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(encoded), nil
	default:
		if b, ok := value.([]byte); ok {
			return string(b), nil
		}
		return value, nil
	}
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
}

func parseTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", raw)
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func columnList(table tableSpec) string {
	columns := make([]string, len(table.columns))
	for i, column := range table.columns {
		columns[i] = quoteIdent(column.name)
	}
	return strings.Join(columns, ",")
}

func quoteIdent(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
