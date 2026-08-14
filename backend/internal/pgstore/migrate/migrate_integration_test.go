package migrate_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"companion-server/internal/pgstore"
	pgmigrate "companion-server/internal/pgstore/migrate"
	"companion-server/internal/store"
)

func TestSQLiteToPostgresFullParity(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_POSTGRES_MIGRATION_TEST_DSN"))
	if dsn == "" { t.Skip("COMPANION_POSTGRES_MIGRATION_TEST_DSN not set") }
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pool, err := pgstore.Open(ctx, pgstore.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil { t.Fatal(err) }
	defer pool.Close()
	resetPostgres(t, ctx, pool)

	path := filepath.Join(t.TempDir(), "companion.sqlite")
	current, err := store.Open(path)
	if err != nil { t.Fatal(err) }
	if err := current.Close(); err != nil { t.Fatal(err) }

	source, err := sql.Open("sqlite", path)
	if err != nil { t.Fatal(err) }
	defer source.Close()
	seedSQLite(t, source)

	report, err := pgmigrate.ImportSQLite(ctx, source, pool)
	if err != nil { t.Fatal(err) }
	if len(report.Tables) != 24 { t.Fatalf("table coverage=%d want=24", len(report.Tables)) }
	for table, digest := range report.Tables {
		if digest.Rows == 0 { t.Fatalf("table %s has no parity fixture rows", table) }
		if len(digest.SHA256) != 64 { t.Fatalf("table %s invalid digest %q", table, digest.SHA256) }
	}

	if _, err := pgmigrate.ImportSQLite(ctx, source, pool); err == nil || !strings.Contains(err.Error(), "not fresh") {
		t.Fatalf("second import must fail closed on non-fresh target, got %v", err)
	}
}

func resetPostgres(t *testing.T, ctx context.Context, pool interface{ Exec(context.Context, string, ...any) (any, error) }) {
	t.Helper()
	// This helper is intentionally test-only. Production migration never truncates a target.
	const truncate = `TRUNCATE TABLE
turn_results,notes,expenses,journal_entries,reminders,conversation_messages,budgets,voice_memos,
idempotency_records,legacy_idempotency_reservations,memory_vectors,memories,device_twins,config_overrides,
config_generation,feature_flags,entitlements,device_credentials,outbox,market_watches,firmware_releases,llm_usage,
privacy_policies,feature_modules RESTART IDENTITY CASCADE`
	if _, err := pool.Exec(ctx, truncate); err != nil { t.Fatal(err) }
	if _, err := pool.Exec(ctx, `INSERT INTO config_generation(id,version) VALUES(1,1)`); err != nil { t.Fatal(err) }
}

func seedSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	statements := []string{
		`INSERT INTO turn_results(turn_id,response,created_at) VALUES('turn-1','{"ok":true}','2026-08-14T19:00:00+07:00')`,
		`INSERT INTO notes(id,idempotency_key,user_id,content,created_at) VALUES(11,'note-key','u1','xin chào','2026-08-14T19:01:00+07:00')`,
		`INSERT INTO expenses(id,idempotency_key,user_id,amount_vnd,category,description,occurred_at,created_at) VALUES(12,'expense-key','u1',50000,'food','cơm','2026-08-14T12:02:00Z','2026-08-14T19:02:01+07:00')`,
		`INSERT INTO journal_entries(id,idempotency_key,user_id,content,occurred_at,created_at) VALUES(13,'journal-key','u1','ngày tốt','2026-08-14T19:03:00+07:00','2026-08-14T19:03:01+07:00')`,
		`INSERT INTO reminders(id,idempotency_key,user_id,device_id,kind,title,fire_at,status,attempts,next_attempt_at,paused_remaining_seconds,created_at) VALUES(14,'rem-key','u1','dev1','reminder','gọi mẹ','2026-08-15T08:00:00+07:00','pending',2,'',0,'2026-08-14T19:04:00+07:00')`,
		`INSERT INTO conversation_messages(id,turn_key,user_id,thread_id,role,content,created_at) VALUES(15,'turn-key','u1','thread-1','user','hello','2026-08-14T19:05:00+07:00')`,
		`INSERT INTO budgets(user_id,period,limit_vnd,updated_at) VALUES('u1','weekly',1000000,'2026-08-14T19:06:00+07:00')`,
		`INSERT INTO voice_memos(id,idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES(16,'memo-key','u1','dev1','data/recordings/memo.wav','ghi âm',1234,'2026-08-14T19:07:00+07:00')`,
		`INSERT INTO idempotency_records(actor_id,operation,idempotency_key,request_hash,outcome_json,created_at) VALUES('u1','note.create','idem-1','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{"z":2, "a":1}','2026-08-14T19:08:00+07:00')`,
		`INSERT INTO legacy_idempotency_reservations(operation,idempotency_key,source_table,created_at) VALUES('expense.log','legacy-1','expenses','2026-08-14T19:09:00+07:00')`,
		`INSERT INTO memories(id,user_id,memory_key,kind,value,valid_from,valid_to,source,confidence,embedding,created_at,deleted_at) VALUES(17,'u1','language','semantic','Vietnamese','2026-08-14T19:10:00+07:00',NULL,'voice',0.9,'[0.1, 0.2]','2026-08-14T19:10:01+07:00',NULL)`,
		`INSERT INTO memory_vectors(memory_id,user_id,embedding,updated_at) VALUES(17,'u1','[0.1,0.2]','2026-08-14T19:10:02+07:00')`,
		`INSERT INTO device_twins(device_id,user_id,desired_json,desired_version,reported_json,reported_version,updated_at) VALUES('dev1','u1','{"volume":42}',3,'{"volume":40}',2,'2026-08-14T19:11:00+07:00')`,
		`UPDATE device_twins SET desired_json='{"volume":43}',desired_version=4,updated_at='2026-08-14T19:11:01+07:00' WHERE device_id='dev1'`,
		`INSERT INTO config_overrides(scope_type,scope_id,config_json,version,updated_at) VALUES('device','dev1','{"b":2,"a":1}',7,'2026-08-14T19:12:00+07:00')`,
		`UPDATE config_generation SET version=9 WHERE id=1`,
		`INSERT INTO feature_flags(key,enabled,rollout,required_plan,variants_json,lifecycle,owner,expires_at,updated_at) VALUES('feature.x',1,75,'pro','{"beta":true}','released','team',NULL,'2026-08-14T19:13:00+07:00')`,
		`INSERT INTO entitlements(subject_type,subject_id,entitlement,enabled,expires_at,updated_at) VALUES('user','u1','voice',0,NULL,'2026-08-14T19:14:00+07:00')`,
		`INSERT INTO device_credentials(device_id,user_id,tenant_id,plan,token_sha256,status,created_at,rotated_at) VALUES('dev1','u1','tenant1','pro','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','active','2026-08-14T19:15:00+07:00','2026-08-14T19:15:01+07:00')`,
		`INSERT INTO market_watches(id,idempotency_key,user_id,device_id,provider,symbol,currency,operator,threshold,enabled,last_state,created_at) VALUES(18,'watch-key','u1','dev1','provider','GOLD','VND','>=',123.45,1,0,'2026-08-14T19:16:00+07:00')`,
		`INSERT INTO firmware_releases(metadata_version,version,channel,board,protocol_min,security_version,url,sha256,size,expires_at,signature,manifest_json,created_at) VALUES(3,'1.2.3','stable','esp32-s3',2,4,'https://example.invalid/fw.bin','cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc',4096,'2026-09-14T19:17:00+07:00','sig','{"board":"esp32-s3","version":"1.2.3"}','2026-08-14T19:17:00+07:00')`,
		`INSERT INTO llm_usage(id,user_id,device_id,provider,model,prompt_version,prompt_tokens,completion_tokens,total_tokens,created_at) VALUES(19,'u1','dev1','local','model','p1',10,5,15,'2026-08-14T19:18:00+07:00')`,
		`INSERT INTO privacy_policies(user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at) VALUES('u1',0,1,30,7,90,'2026-08-14T19:19:00+07:00')`,
		`INSERT INTO feature_modules(id,version,lifecycle,execution,manifest_json,updated_at) VALUES('module.x',2,'released','backend','{"permissions":["read"]}','2026-08-14T19:20:00+07:00')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil { t.Fatalf("seed SQLite statement failed: %v\n%s", err, statement) }
	}
	// Outbox is covered by real current SQLite triggers above. Verify this fixture
	// actually exercises event parity instead of silently importing an empty table.
	var outboxCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox`).Scan(&outboxCount); err != nil { t.Fatal(err) }
	if outboxCount == 0 { t.Fatal("fixture produced no outbox rows") }
}
