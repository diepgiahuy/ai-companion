package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/events"
	"companion-server/internal/market"
	"companion-server/internal/memory"
	"companion-server/internal/privacy"
	"companion-server/internal/usage"
)

func (s *Store) migratePlatform() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS memories (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id TEXT NOT NULL,
            memory_key TEXT NOT NULL,
            kind TEXT NOT NULL,
            value TEXT NOT NULL,
            valid_from TEXT NOT NULL,
            valid_to TEXT,
            source TEXT NOT NULL,
            confidence REAL NOT NULL DEFAULT 1,
            embedding TEXT NOT NULL DEFAULT '[]',
            created_at TEXT NOT NULL,
            deleted_at TEXT
        )`,
		`CREATE INDEX IF NOT EXISTS idx_memories_current ON memories(user_id,memory_key,valid_to,deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_user_time ON memories(user_id,valid_from DESC)`,
		`CREATE TABLE IF NOT EXISTS memory_vectors (memory_id INTEGER PRIMARY KEY,user_id TEXT NOT NULL,embedding TEXT NOT NULL,updated_at TEXT NOT NULL,FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_vectors_user ON memory_vectors(user_id,memory_id)`,
		`CREATE TABLE IF NOT EXISTS device_twins (
            device_id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            desired_json TEXT NOT NULL DEFAULT '{}',
            desired_version INTEGER NOT NULL DEFAULT 0,
            reported_json TEXT NOT NULL DEFAULT '{}',
            reported_version INTEGER NOT NULL DEFAULT 0,
            updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS config_overrides (
            scope_type TEXT NOT NULL,
            scope_id TEXT NOT NULL,
            config_json TEXT NOT NULL DEFAULT '{}',
            version INTEGER NOT NULL DEFAULT 1,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(scope_type,scope_id)
        )`,
		`CREATE TABLE IF NOT EXISTS config_generation (id INTEGER PRIMARY KEY CHECK(id=1),version INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO config_generation(id,version) VALUES(1,1)`,
		`CREATE TABLE IF NOT EXISTS feature_flags (
            key TEXT PRIMARY KEY,
            enabled INTEGER NOT NULL DEFAULT 0,
            rollout INTEGER NOT NULL DEFAULT 100 CHECK(rollout>=0 AND rollout<=100),
            required_plan TEXT NOT NULL DEFAULT '',
            variants_json TEXT NOT NULL DEFAULT '{}',
            lifecycle TEXT NOT NULL DEFAULT 'released',
            owner TEXT NOT NULL DEFAULT '',
            expires_at TEXT,
            updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS entitlements (
            subject_type TEXT NOT NULL DEFAULT 'user',
            subject_id TEXT NOT NULL,
            entitlement TEXT NOT NULL,
            enabled INTEGER NOT NULL DEFAULT 1,
            expires_at TEXT,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(subject_type,subject_id,entitlement)
        )`,
		`CREATE TABLE IF NOT EXISTS device_credentials (
            device_id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            tenant_id TEXT NOT NULL DEFAULT '',
            plan TEXT NOT NULL DEFAULT '',
            token_sha256 TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'active',
            created_at TEXT NOT NULL,
            rotated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS outbox (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            event_id TEXT NOT NULL UNIQUE,
            source TEXT NOT NULL,
            event_type TEXT NOT NULL,
            subject TEXT NOT NULL DEFAULT '',
            user_id TEXT NOT NULL DEFAULT '',
            data_json TEXT NOT NULL DEFAULT '{}',
            occurred_at TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'pending',
            attempts INTEGER NOT NULL DEFAULT 0,
            next_attempt_at TEXT NOT NULL,
            last_error TEXT NOT NULL DEFAULT ''
        )`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_dispatch ON outbox(status,next_attempt_at,id)`,
		`CREATE TABLE IF NOT EXISTS market_watches (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            idempotency_key TEXT NOT NULL UNIQUE,
            user_id TEXT NOT NULL,
            device_id TEXT NOT NULL,
            provider TEXT NOT NULL,
            symbol TEXT NOT NULL,
            currency TEXT NOT NULL,
            operator TEXT NOT NULL CHECK(operator IN ('<','<=','>','>=')),
            threshold REAL NOT NULL,
            enabled INTEGER NOT NULL DEFAULT 1,
            last_state INTEGER NOT NULL DEFAULT 0,
            created_at TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_market_watches_enabled ON market_watches(enabled,provider,symbol)`,
		`CREATE TABLE IF NOT EXISTS firmware_releases (
            metadata_version INTEGER PRIMARY KEY,
            version TEXT NOT NULL,
            channel TEXT NOT NULL,
            board TEXT NOT NULL,
            protocol_min INTEGER NOT NULL,
            security_version INTEGER NOT NULL,
            url TEXT NOT NULL,
            sha256 TEXT NOT NULL,
            size INTEGER NOT NULL,
            expires_at TEXT NOT NULL,
            signature TEXT NOT NULL DEFAULT '',
            manifest_json TEXT NOT NULL,
            created_at TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_firmware_lookup ON firmware_releases(channel,board,metadata_version DESC)`,
		`CREATE TABLE IF NOT EXISTS llm_usage (
            id INTEGER PRIMARY KEY AUTOINCREMENT,user_id TEXT NOT NULL DEFAULT '',device_id TEXT NOT NULL DEFAULT '',
            provider TEXT NOT NULL,model TEXT NOT NULL,prompt_version TEXT NOT NULL,prompt_tokens INTEGER NOT NULL,
            completion_tokens INTEGER NOT NULL,total_tokens INTEGER NOT NULL,created_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS privacy_policies (
            user_id TEXT PRIMARY KEY,save_voice_audio INTEGER NOT NULL DEFAULT 1,long_term_memory_enabled INTEGER NOT NULL DEFAULT 1,
            conversation_retention_days INTEGER NOT NULL DEFAULT 0,voice_memo_retention_days INTEGER NOT NULL DEFAULT 0,memory_retention_days INTEGER NOT NULL DEFAULT 0,updated_at TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS feature_modules (
            id TEXT PRIMARY KEY,version INTEGER NOT NULL,lifecycle TEXT NOT NULL,execution TEXT NOT NULL,manifest_json TEXT NOT NULL,updated_at TEXT NOT NULL
        )`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("platform migration: %w", err)
		}
	}
	for _, c := range []struct{ table, column, alter string }{
		{"device_credentials", "tenant_id", `ALTER TABLE device_credentials ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`},
		{"device_credentials", "plan", `ALTER TABLE device_credentials ADD COLUMN plan TEXT NOT NULL DEFAULT ''`},
	} {
		if err := s.ensureColumn(c.table, c.column, c.alter); err != nil {
			return err
		}
	}
	// Event envelopes are generated by SQLite in the same transaction as the
	// authoritative state mutation. JSON1's json_object safely quotes strings.
	triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_expenses_ai AFTER INSERT ON expenses BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain','expense.created','expense/'||NEW.id,NEW.user_id,json_object('id',NEW.id,'amount_vnd',NEW.amount_vnd,'category',NEW.category),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_expenses_au AFTER UPDATE ON expenses BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain','expense.updated','expense/'||NEW.id,NEW.user_id,json_object('id',NEW.id,'amount_vnd',NEW.amount_vnd,'category',NEW.category),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_expenses_ad AFTER DELETE ON expenses BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain','expense.deleted','expense/'||OLD.id,OLD.user_id,json_object('id',OLD.id),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_budgets_ai AFTER INSERT ON budgets BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain','budget.updated','budget/'||NEW.period,NEW.user_id,json_object('period',NEW.period,'limit_vnd',NEW.limit_vnd),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_budgets_au AFTER UPDATE ON budgets BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain','budget.updated','budget/'||NEW.period,NEW.user_id,json_object('period',NEW.period,'limit_vnd',NEW.limit_vnd),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_reminders_ai AFTER INSERT ON reminders BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/domain',NEW.kind||'.created',NEW.kind||'/'||NEW.id,NEW.user_id,json_object('id',NEW.id,'device_id',NEW.device_id,'fire_at',NEW.fire_at),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_memories_ai AFTER INSERT ON memories BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/memory','memory.created','memory/'||NEW.id,NEW.user_id,json_object('id',NEW.id,'key',NEW.memory_key,'kind',NEW.kind),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_twins_au AFTER UPDATE ON device_twins WHEN NEW.desired_version!=OLD.desired_version BEGIN INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(lower(hex(randomblob(16))),'/companion/control','device.config.updated','device/'||NEW.device_id,NEW.user_id,json_object('device_id',NEW.device_id,'version',NEW.desired_version),strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')); END`,
	}
	for _, q := range triggers {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("platform trigger migration: %w", err)
		}
	}
	return nil
}

// ---- Temporal / semantic memory ----
func (s *Store) UpsertMemory(ctx context.Context, m memory.Item) (memory.Item, error) {
	if m.UserID == "" || m.Key == "" || m.Value == "" {
		return m, fmt.Errorf("user, key and value required")
	}
	emb, _ := json.Marshal(m.Embedding)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	vf := m.ValidFrom.UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE memories SET valid_to=? WHERE user_id=? AND memory_key=? AND valid_to IS NULL AND deleted_at IS NULL`, vf, m.UserID, m.Key); err != nil {
		return m, err
	}
	created := m.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO memories(user_id,memory_key,kind,value,valid_from,source,confidence,embedding,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.UserID, m.Key, string(m.Kind), m.Value, vf, m.Source, m.Confidence, string(emb), created.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return m, err
	}
	id, _ := res.LastInsertId()
	m.ID = id
	m.CreatedAt = created
	if err = tx.Commit(); err != nil {
		return m, err
	}
	return m, nil
}
func (s *Store) CurrentMemories(ctx context.Context, user string, now time.Time, limit int) ([]memory.Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,memory_key,kind,value,valid_from,valid_to,source,confidence,embedding,created_at FROM memories WHERE user_id=? AND deleted_at IS NULL AND valid_from<=? AND (valid_to IS NULL OR valid_to>?) ORDER BY valid_from DESC LIMIT ?`, user, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memory.Item
	for rows.Next() {
		var m memory.Item
		var kind, vf, emb, created string
		var vtNull sql.NullString
		if err := rows.Scan(&m.ID, &m.UserID, &m.Key, &kind, &m.Value, &vf, &vtNull, &m.Source, &m.Confidence, &emb, &created); err != nil {
			return nil, err
		}
		m.Kind = memory.Kind(kind)
		m.ValidFrom, _ = time.Parse(time.RFC3339Nano, vf)
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if vtNull.Valid {
			z, _ := time.Parse(time.RFC3339Nano, vtNull.String)
			m.ValidTo = &z
		}
		_ = json.Unmarshal([]byte(emb), &m.Embedding)
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) ForgetMemory(ctx context.Context, user, key string) error {
	r, e := s.db.ExecContext(ctx, `UPDATE memories SET deleted_at=?,valid_to=COALESCE(valid_to,?) WHERE user_id=? AND memory_key=? AND deleted_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), user, key)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory key not found")
	}
	return nil
}

// ---- Device twin / feature config ----
func (s *Store) ensureTwin(ctx context.Context, user, device string) error {
	if device == "" {
		return fmt.Errorf("device_id required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, e := s.db.ExecContext(ctx, `INSERT INTO device_twins(device_id,user_id,updated_at) VALUES(?,?,?) ON CONFLICT(device_id) DO NOTHING`, device, user, now)
	return e
}
func (s *Store) GetTwin(ctx context.Context, user, device string) (controlplane.Twin, error) {
	if e := s.ensureTwin(ctx, user, device); e != nil {
		return controlplane.Twin{}, e
	}
	var t controlplane.Twin
	var desired, reported, updated string
	if e := s.db.QueryRowContext(ctx, `SELECT device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at FROM device_twins WHERE device_id=?`, device).Scan(&t.DeviceID, &t.UserID, &desired, &t.DesiredVersion, &reported, &t.ReportedVersion, &updated); e != nil {
		return t, e
	}
	if t.UserID != user && user != "" {
		return t, fmt.Errorf("device owner mismatch")
	}
	t.Desired, _ = controlplane.Decode(desired)
	t.Reported, _ = controlplane.Decode(reported)
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if generation, err := s.ConfigGeneration(ctx); err == nil && generation > t.DesiredVersion {
		t.DesiredVersion = generation
	}
	return t, nil
}
func (s *Store) SetDesired(ctx context.Context, user, device string, c controlplane.RuntimeConfig) (controlplane.Twin, error) {
	if e := s.ensureTwin(ctx, user, device); e != nil {
		return controlplane.Twin{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return controlplane.Twin{}, e
	}
	defer tx.Rollback()
	generation, e := nextConfigGeneration(ctx, tx)
	if e != nil {
		return controlplane.Twin{}, e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r, e := tx.ExecContext(ctx, `UPDATE device_twins SET desired_json=?,desired_version=?,updated_at=? WHERE device_id=? AND user_id=?`, controlplane.Encode(c), generation, now, device, user)
	if e != nil {
		return controlplane.Twin{}, e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return controlplane.Twin{}, fmt.Errorf("device owner mismatch")
	}
	if e := tx.Commit(); e != nil {
		return controlplane.Twin{}, e
	}
	return s.GetTwin(ctx, user, device)
}
func (s *Store) Report(ctx context.Context, user, device string, v int64, c controlplane.RuntimeConfig) error {
	if e := s.ensureTwin(ctx, user, device); e != nil {
		return e
	}
	t, e := s.GetTwin(ctx, user, device)
	if e != nil {
		return e
	}
	if v < t.ReportedVersion {
		return nil
	}
	if v > t.DesiredVersion {
		return fmt.Errorf("reported config version ahead of desired")
	}
	_, e = s.db.ExecContext(ctx, `UPDATE device_twins SET reported_json=?,reported_version=?,updated_at=? WHERE device_id=? AND user_id=?`, controlplane.Encode(c), v, time.Now().UTC().Format(time.RFC3339Nano), device, user)
	return e
}
func (s *Store) Flags(ctx context.Context) ([]controlplane.Flag, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at FROM feature_flags`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []controlplane.Flag
	for rows.Next() {
		var x controlplane.Flag
		var en int
		var variants string
		var exp sql.NullString
		if e := rows.Scan(&x.Key, &en, &x.Rollout, &x.RequiredPlan, &variants, &x.Lifecycle, &x.Owner, &exp); e != nil {
			return nil, e
		}
		x.Enabled = en != 0
		if exp.Valid {
			if z, e := time.Parse(time.RFC3339Nano, exp.String); e == nil {
				x.ExpiresAt = &z
			}
		}
		_ = json.Unmarshal([]byte(variants), &x.Variants)
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) SetFlag(ctx context.Context, f controlplane.Flag) error {
	if f.Key == "" || f.Rollout < 0 || f.Rollout > 100 {
		return fmt.Errorf("invalid flag")
	}
	if f.Lifecycle == "" {
		f.Lifecycle = "released"
	}
	v, _ := json.Marshal(f.Variants)
	var exp any
	if f.ExpiresAt != nil {
		exp = f.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, e := s.db.ExecContext(ctx, `INSERT INTO feature_flags(key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET enabled=excluded.enabled,rollout=excluded.rollout,required_plan=excluded.required_plan,variants_json=excluded.variants_json,lifecycle=excluded.lifecycle,owner=excluded.owner,expires_at=excluded.expires_at,updated_at=excluded.updated_at`, f.Key, boolInt(f.Enabled), f.Rollout, f.RequiredPlan, string(v), f.Lifecycle, f.Owner, exp, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}

// ---- Device credentials. Raw tokens are never stored. ----
func (s *Store) EnrollDevice(ctx context.Context, identity domain.Identity, rawToken string) error {
	if identity.UserID == "" || identity.DeviceID == "" || len(rawToken) < 16 {
		return fmt.Errorf("user/device and token>=16 required")
	}
	h := sha256.Sum256([]byte(rawToken))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, e := s.db.ExecContext(ctx, `INSERT INTO device_credentials(device_id,user_id,tenant_id,plan,token_sha256,status,created_at,rotated_at) VALUES(?,?,?,?,?,'active',?,?) ON CONFLICT(device_id) DO UPDATE SET user_id=excluded.user_id,tenant_id=excluded.tenant_id,plan=excluded.plan,token_sha256=excluded.token_sha256,status='active',rotated_at=excluded.rotated_at`, identity.DeviceID, identity.UserID, identity.TenantID, identity.Plan, hex.EncodeToString(h[:]), now, now)
	return e
}
func (s *Store) AuthenticateDevice(ctx context.Context, device, rawToken string) (domain.Identity, bool, error) {
	h := sha256.Sum256([]byte(rawToken))
	var identity domain.Identity
	identity.DeviceID = device
	var status, stored string
	e := s.db.QueryRowContext(ctx, `SELECT user_id,tenant_id,plan,status,token_sha256 FROM device_credentials WHERE device_id=?`, device).Scan(&identity.UserID, &identity.TenantID, &identity.Plan, &status, &stored)
	if errors.Is(e, sql.ErrNoRows) {
		return domain.Identity{}, false, nil
	}
	if e != nil {
		return domain.Identity{}, false, e
	}
	return identity, status == "active" && subtle.ConstantTimeCompare([]byte(stored), []byte(hex.EncodeToString(h[:]))) == 1, nil
}
func (s *Store) RevokeDevice(ctx context.Context, device string) error {
	_, e := s.db.ExecContext(ctx, `UPDATE device_credentials SET status='revoked' WHERE device_id=?`, device)
	return e
}

// ---- Transactional outbox ----
func (s *Store) Enqueue(ctx context.Context, e events.Event) error {
	if e.ID == "" {
		return fmt.Errorf("event id required")
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at) VALUES(?,?,?,?,?,?,?,?)`, e.ID, e.Source, e.Type, e.Subject, e.UserID, string(e.Data), e.Time.UTC().Format(time.RFC3339Nano), e.Time.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) Claim(ctx context.Context, now time.Time, limit int) ([]events.Pending, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,event_id,source,event_type,subject,user_id,data_json,occurred_at,attempts,next_attempt_at FROM outbox WHERE status='pending' AND next_attempt_at<=? ORDER BY id LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if e != nil {
		return nil, e
	}
	var xs []events.Pending
	for rows.Next() {
		var p events.Pending
		var data, occurred, next string
		if e := rows.Scan(&p.RowID, &p.Event.ID, &p.Event.Source, &p.Event.Type, &p.Event.Subject, &p.Event.UserID, &data, &occurred, &p.Attempts, &next); e != nil {
			rows.Close()
			return nil, e
		}
		p.Event.Data = json.RawMessage(data)
		p.Event.Time, _ = time.Parse(time.RFC3339Nano, occurred)
		p.NextAttempt, _ = time.Parse(time.RFC3339Nano, next)
		xs = append(xs, p)
	}
	rows.Close()
	for _, p := range xs {
		if _, e = tx.ExecContext(ctx, `UPDATE outbox SET status='dispatching',attempts=attempts+1 WHERE id=? AND status='pending'`, p.RowID); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return xs, nil
}
func (s *Store) MarkSent(ctx context.Context, id int64) error {
	_, e := s.db.ExecContext(ctx, `UPDATE outbox SET status='sent',last_error='' WHERE id=?`, id)
	return e
}
func (s *Store) Retry(ctx context.Context, id int64, msg string, next time.Time) error {
	_, e := s.db.ExecContext(ctx, `UPDATE outbox SET status='pending',last_error=?,next_attempt_at=? WHERE id=?`, msg, next.UTC().Format(time.RFC3339Nano), id)
	return e
}
func (s *Store) RecoverOutbox(ctx context.Context) error {
	_, e := s.db.ExecContext(ctx, `UPDATE outbox SET status='pending' WHERE status='dispatching'`)
	return e
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) RecordUsage(ctx context.Context, u usage.Record) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO llm_usage(user_id,device_id,provider,model,prompt_version,prompt_tokens,completion_tokens,total_tokens,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, u.UserID, u.DeviceID, u.Provider, u.Model, u.PromptVersion, u.PromptTokens, u.CompletionTokens, u.TotalTokens, time.Now().UTC().Format(time.RFC3339Nano))
}
func (s *Store) EnsureFlag(ctx context.Context, f controlplane.Flag) error {
	v, _ := json.Marshal(f.Variants)
	if f.Lifecycle == "" {
		f.Lifecycle = "released"
	}
	var exp any
	if f.ExpiresAt != nil {
		exp = f.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, e := s.db.ExecContext(ctx, `INSERT INTO feature_flags(key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO NOTHING`, f.Key, boolInt(f.Enabled), f.Rollout, f.RequiredPlan, string(v), f.Lifecycle, f.Owner, exp, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}

func (s *Store) CreateMarketWatch(ctx context.Context, user, device, key, provider, symbol, currency, operator string, threshold float64) (market.Watch, error) {
	if err := market.ValidateOperator(operator); err != nil {
		return market.Watch{}, err
	}
	if threshold <= 0 {
		return market.Watch{}, fmt.Errorf("threshold must be positive")
	}
	now := time.Now().UTC()
	_, e := s.db.ExecContext(ctx, `INSERT INTO market_watches(idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(idempotency_key) DO NOTHING`, key, user, device, provider, symbol, currency, operator, threshold, now.Format(time.RFC3339Nano))
	if e != nil {
		return market.Watch{}, e
	}
	var w market.Watch
	var en, last int
	var created string
	e = s.db.QueryRowContext(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE idempotency_key=?`, key).Scan(&w.ID, &w.UserID, &w.DeviceID, &w.Provider, &w.Symbol, &w.Currency, &w.Operator, &w.Threshold, &en, &last, &created)
	w.Enabled = en != 0
	w.LastState = last != 0
	w.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return w, e
}
func (s *Store) ListMarketWatches(ctx context.Context, user, device string, limit int) ([]market.Watch, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE user_id=?`
	args := []any{user}
	if device != "" {
		q += ` AND device_id=?`
		args = append(args, device)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return s.scanWatches(ctx, q, args...)
}
func (s *Store) EnabledMarketWatches(ctx context.Context, limit int) ([]market.Watch, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.scanWatches(ctx, `SELECT id,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at FROM market_watches WHERE enabled=1 ORDER BY id LIMIT ?`, limit)
}
func (s *Store) scanWatches(ctx context.Context, q string, args ...any) ([]market.Watch, error) {
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []market.Watch
	for rows.Next() {
		var w market.Watch
		var en, last int
		var created string
		if e := rows.Scan(&w.ID, &w.UserID, &w.DeviceID, &w.Provider, &w.Symbol, &w.Currency, &w.Operator, &w.Threshold, &en, &last, &created); e != nil {
			return nil, e
		}
		w.Enabled = en != 0
		w.LastState = last != 0
		w.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, w)
	}
	return out, rows.Err()
}
func (s *Store) DeleteMarketWatch(ctx context.Context, user string, id int64) error {
	r, e := s.db.ExecContext(ctx, `DELETE FROM market_watches WHERE id=? AND user_id=?`, id, user)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return fmt.Errorf("market watch not found")
	}
	return nil
}
func (s *Store) SetMarketWatchState(ctx context.Context, id int64, state bool) error {
	_, e := s.db.ExecContext(ctx, `UPDATE market_watches SET last_state=? WHERE id=?`, boolInt(state), id)
	return e
}

func (s *Store) GetConfigOverride(ctx context.Context, scopeType, scopeID string) (controlplane.RuntimeConfig, bool, error) {
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT config_json FROM config_overrides WHERE scope_type=? AND scope_id=?`, scopeType, scopeID).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return controlplane.RuntimeConfig{}, false, nil
	}
	if e != nil {
		return controlplane.RuntimeConfig{}, false, e
	}
	c, e := controlplane.Decode(raw)
	return c, true, e
}
func (s *Store) SetConfigOverride(ctx context.Context, scopeType, scopeID string, c controlplane.RuntimeConfig) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = nextConfigGeneration(ctx, tx); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO config_overrides(scope_type,scope_id,config_json,version,updated_at) VALUES(?,?,?,1,?) ON CONFLICT(scope_type,scope_id) DO UPDATE SET config_json=excluded.config_json,version=config_overrides.version+1,updated_at=excluded.updated_at`, scopeType, scopeID, controlplane.Encode(c), time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SetEntitlement(ctx context.Context, user, key string, enabled bool, expires *time.Time) error {
	var exp any
	if expires != nil {
		exp = expires.UTC().Format(time.RFC3339Nano)
	}
	_, e := s.db.ExecContext(ctx, `INSERT INTO entitlements(subject_type,subject_id,entitlement,enabled,expires_at,updated_at) VALUES('user',?,?,?,?,?) ON CONFLICT(subject_type,subject_id,entitlement) DO UPDATE SET enabled=excluded.enabled,expires_at=excluded.expires_at,updated_at=excluded.updated_at`, user, key, boolInt(enabled), exp, time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) Allowed(ctx context.Context, user, key string) bool {
	var enabled int
	var exp sql.NullString
	e := s.db.QueryRowContext(ctx, `SELECT enabled,expires_at FROM entitlements WHERE subject_type='user' AND subject_id=? AND entitlement=?`, user, key).Scan(&enabled, &exp)
	if e != nil || enabled == 0 {
		return false
	}
	if exp.Valid {
		t, e := time.Parse(time.RFC3339Nano, exp.String)
		if e != nil || !t.After(time.Now()) {
			return false
		}
	}
	return true
}

func (s *Store) UpsertVector(ctx context.Context, user string, id int64, v []float32) error {
	b, _ := json.Marshal(v)
	_, e := s.db.ExecContext(ctx, `INSERT INTO memory_vectors(memory_id,user_id,embedding,updated_at) VALUES(?,?,?,?) ON CONFLICT(memory_id) DO UPDATE SET user_id=excluded.user_id,embedding=excluded.embedding,updated_at=excluded.updated_at`, id, user, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) DeleteVector(ctx context.Context, user string, id int64) error {
	_, e := s.db.ExecContext(ctx, `DELETE FROM memory_vectors WHERE memory_id=? AND user_id=?`, id, user)
	return e
}
func (s *Store) SearchVectors(ctx context.Context, user string, q []float32, limit int) ([]memory.VectorHit, error) {
	if limit <= 0 || limit > 500 {
		limit = 80
	}
	rows, e := s.db.QueryContext(ctx, `SELECT memory_id,embedding FROM memory_vectors WHERE user_id=?`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []memory.VectorHit
	for rows.Next() {
		var id int64
		var raw string
		if e := rows.Scan(&id, &raw); e != nil {
			return nil, e
		}
		var v []float32
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		score := vectorCos(q, v)
		out = append(out, memory.VectorHit{ID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, rows.Err()
}
func vectorCos(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func (s *Store) PutFirmware(ctx context.Context, m controlplane.FirmwareManifest) error {
	b, _ := json.Marshal(m)
	_, e := s.db.ExecContext(ctx, `INSERT INTO firmware_releases(metadata_version,version,channel,board,protocol_min,security_version,url,sha256,size,expires_at,signature,manifest_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, m.MetadataVersion, m.Version, m.Channel, m.Board, m.ProtocolMin, m.SecurityVersion, m.URL, m.SHA256, m.Size, m.ExpiresAt.UTC().Format(time.RFC3339Nano), m.Signature, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) LatestFirmware(ctx context.Context, channel, board string, protocol, currentSecurity int, now time.Time) (controlplane.FirmwareManifest, bool, error) {
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT manifest_json FROM firmware_releases WHERE channel=? AND board=? AND protocol_min<=? AND security_version>=? AND expires_at>? ORDER BY metadata_version DESC LIMIT 1`, channel, board, protocol, currentSecurity, now.UTC().Format(time.RFC3339Nano)).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return controlplane.FirmwareManifest{}, false, nil
	}
	if e != nil {
		return controlplane.FirmwareManifest{}, false, e
	}
	var m controlplane.FirmwareManifest
	if e = json.Unmarshal([]byte(raw), &m); e != nil {
		return m, false, e
	}
	return m, true, nil
}

// ---- User privacy / retention ----
func (s *Store) GetPrivacyPolicy(ctx context.Context, user string) (privacy.Policy, bool, error) {
	var p privacy.Policy
	var save, mem int
	var updated string
	err := s.db.QueryRowContext(ctx, `SELECT user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at FROM privacy_policies WHERE user_id=?`, owner(user)).Scan(&p.UserID, &save, &mem, &p.ConversationRetentionDays, &p.VoiceMemoRetentionDays, &p.MemoryRetentionDays, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return privacy.Policy{}, false, nil
	}
	if err != nil {
		return privacy.Policy{}, false, err
	}
	p.SaveVoiceAudio, p.LongTermMemoryEnabled = save != 0, mem != 0
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return p, true, nil
}
func (s *Store) SetPrivacyPolicy(ctx context.Context, p privacy.Policy) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO privacy_policies(user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET save_voice_audio=excluded.save_voice_audio,long_term_memory_enabled=excluded.long_term_memory_enabled,conversation_retention_days=excluded.conversation_retention_days,voice_memo_retention_days=excluded.voice_memo_retention_days,memory_retention_days=excluded.memory_retention_days,updated_at=excluded.updated_at`, owner(p.UserID), boolInt(p.SaveVoiceAudio), boolInt(p.LongTermMemoryEnabled), p.ConversationRetentionDays, p.VoiceMemoRetentionDays, p.MemoryRetentionDays, p.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) ApplyRetention(ctx context.Context, now time.Time) (privacy.RetentionReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,conversation_retention_days,voice_memo_retention_days,memory_retention_days FROM privacy_policies WHERE conversation_retention_days>0 OR voice_memo_retention_days>0 OR memory_retention_days>0`)
	if err != nil {
		return privacy.RetentionReport{}, err
	}
	type entry struct {
		user             string
		conv, voice, mem int
	}
	var policies []entry
	for rows.Next() {
		var x entry
		if err := rows.Scan(&x.user, &x.conv, &x.voice, &x.mem); err != nil {
			rows.Close()
			return privacy.RetentionReport{}, err
		}
		policies = append(policies, x)
	}
	if err := rows.Close(); err != nil {
		return privacy.RetentionReport{}, err
	}
	var report privacy.RetentionReport
	for _, p := range policies {
		if p.conv > 0 {
			cut := now.AddDate(0, 0, -p.conv).UTC().Format(time.RFC3339Nano)
			res, e := s.db.ExecContext(ctx, `DELETE FROM conversation_messages WHERE user_id=? AND created_at<?`, p.user, cut)
			if e != nil {
				return report, e
			}
			n, _ := res.RowsAffected()
			report.ConversationRows += int(n)
		}
		if p.mem > 0 {
			cut := now.AddDate(0, 0, -p.mem).UTC().Format(time.RFC3339Nano)
			res, e := s.db.ExecContext(ctx, `DELETE FROM memories WHERE user_id=? AND created_at<?`, p.user, cut)
			if e != nil {
				return report, e
			}
			n, _ := res.RowsAffected()
			report.MemoryRows += int(n)
		}
		if p.voice > 0 {
			cut := now.AddDate(0, 0, -p.voice).UTC().Format(time.RFC3339Nano)
			vr, e := s.db.QueryContext(ctx, `SELECT path FROM voice_memos WHERE user_id=? AND created_at<?`, p.user, cut)
			if e != nil {
				return report, e
			}
			for vr.Next() {
				var path string
				if vr.Scan(&path) == nil && path != "" {
					report.OrphanPaths = append(report.OrphanPaths, path)
				}
			}
			vr.Close()
			res, e := s.db.ExecContext(ctx, `DELETE FROM voice_memos WHERE user_id=? AND created_at<?`, p.user, cut)
			if e != nil {
				return report, e
			}
			n, _ := res.RowsAffected()
			report.VoiceMemoRows += int(n)
		}
	}
	return report, nil
}

func (s *Store) PutFeatureModule(ctx context.Context, m controlplane.FeatureModule) error {
	b, _ := json.Marshal(m)
	_, err := s.db.ExecContext(ctx, `INSERT INTO feature_modules(id,version,lifecycle,execution,manifest_json,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET version=excluded.version,lifecycle=excluded.lifecycle,execution=excluded.execution,manifest_json=excluded.manifest_json,updated_at=excluded.updated_at WHERE excluded.version>=feature_modules.version`, m.ID, m.Version, m.Lifecycle, m.Execution, string(b), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) FeatureModule(ctx context.Context, id string) (controlplane.FeatureModule, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT manifest_json FROM feature_modules WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return controlplane.FeatureModule{}, false, nil
	}
	if err != nil {
		return controlplane.FeatureModule{}, false, err
	}
	var m controlplane.FeatureModule
	if err = json.Unmarshal([]byte(raw), &m); err != nil {
		return m, false, err
	}
	return m, true, nil
}
func (s *Store) FeatureModules(ctx context.Context) ([]controlplane.FeatureModule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT manifest_json FROM feature_modules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []controlplane.FeatureModule
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m controlplane.FeatureModule
		if json.Unmarshal([]byte(raw), &m) == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// TriggerMarketWatch transitions false->true and creates the user-visible alert
// atomically. Concurrent workers or a retry after a partial failure cannot
// generate two reminders for the same threshold crossing.
func (s *Store) TriggerMarketWatch(ctx context.Context, w market.Watch, title string, fireAt time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE market_watches SET last_state=1 WHERE id=? AND user_id=? AND enabled=1 AND last_state=0`, w.ID, w.UserID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	key := fmt.Sprintf("market-watch:%d:cross:%d", w.ID, fireAt.UTC().UnixNano())
	_, err = tx.ExecContext(ctx, `INSERT INTO reminders(idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES(?,?,?,?,?,?,'pending',0,'',0,?)`, key, owner(w.UserID), strings.TrimSpace(w.DeviceID), "reminder", strings.TrimSpace(title), fireAt.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func nextConfigGeneration(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE config_generation SET version=version+1 WHERE id=1`); err != nil {
		return 0, err
	}
	var v int64
	err := tx.QueryRowContext(ctx, `SELECT version FROM config_generation WHERE id=1`).Scan(&v)
	return v, err
}
func (s *Store) ConfigGeneration(ctx context.Context) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT version FROM config_generation WHERE id=1`).Scan(&v)
	return v, err
}

func (s *Store) TotalTokensSince(ctx context.Context, user string, since time.Time) (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(total_tokens) FROM llm_usage WHERE user_id=? AND created_at>=?`, owner(user), since.UTC().Format(time.RFC3339Nano)).Scan(&n)
	if err != nil {
		return 0, err
	}
	if !n.Valid {
		return 0, nil
	}
	return n.Int64, nil
}
