package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"companion-server/internal/idempotency"
	"companion-server/internal/memory"
	"companion-server/internal/privacy"
	"companion-server/internal/usage"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertMemory(ctx context.Context, item memory.Item) (memory.Item, error) {
	if item.UserID == "" || item.Key == "" || item.Value == "" {
		return item, fmt.Errorf("user, key and value required")
	}
	if item.ValidFrom.IsZero() {
		item.ValidFrom = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	embedding, err := json.Marshal(item.Embedding)
	if err != nil {
		return item, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return item, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE memories SET valid_to=$1 WHERE user_id=$2 AND memory_key=$3 AND valid_to IS NULL AND deleted_at IS NULL`, item.ValidFrom.UTC(), owner(item.UserID), item.Key); err != nil {
		return item, err
	}
	if err = tx.QueryRow(ctx, `INSERT INTO memories(user_id,memory_key,kind,value,valid_from,source,confidence,embedding,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING id`, owner(item.UserID), item.Key, string(item.Kind), item.Value, item.ValidFrom.UTC(), item.Source, item.Confidence, string(embedding), item.CreatedAt.UTC()).Scan(&item.ID); err != nil {
		return item, err
	}
	if err = tx.Commit(ctx); err != nil {
		return item, err
	}
	item.UserID = owner(item.UserID)
	item.ValidFrom = item.ValidFrom.UTC()
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}

func (s *Store) UpsertMemoryMutation(ctx context.Context, request idempotency.Request, item memory.Item) (memory.Item, error) {
	item.Key = strings.TrimSpace(item.Key)
	item.Value = strings.TrimSpace(item.Value)
	if item.UserID == "" || item.Key == "" || item.Value == "" {
		return item, fmt.Errorf("user, key and value required")
	}
	if item.ValidFrom.IsZero() {
		item.ValidFrom = time.Now().UTC()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	embedding, err := json.Marshal(item.Embedding)
	if err != nil {
		return item, err
	}
	return runMutationValue(ctx, s, request, "memory.remember", func(tx pgx.Tx) (memory.Item, error) {
		item.UserID = owner(item.UserID)
		item.ValidFrom = item.ValidFrom.UTC()
		item.CreatedAt = item.CreatedAt.UTC()
		if _, err := tx.Exec(ctx, `UPDATE memories SET valid_to=$1 WHERE user_id=$2 AND memory_key=$3 AND valid_to IS NULL AND deleted_at IS NULL`, item.ValidFrom, item.UserID, item.Key); err != nil {
			return item, err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO memories(user_id,memory_key,kind,value,valid_from,source,confidence,embedding,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9) RETURNING id`, item.UserID, item.Key, string(item.Kind), item.Value, item.ValidFrom, item.Source, item.Confidence, string(embedding), item.CreatedAt).Scan(&item.ID); err != nil {
			return item, err
		}
		return item, nil
	})
}

func (s *Store) CurrentMemories(ctx context.Context, userID string, now time.Time, limit int) ([]memory.Item, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT id,user_id,memory_key,kind,value,valid_from,valid_to,source,confidence,embedding,created_at FROM memories WHERE user_id=$1 AND deleted_at IS NULL AND valid_from<=$2 AND (valid_to IS NULL OR valid_to>$2) ORDER BY valid_from DESC LIMIT $3`, owner(userID), now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memory.Item
	for rows.Next() {
		var item memory.Item
		var kind string
		var validTo *time.Time
		var raw []byte
		if err := rows.Scan(&item.ID, &item.UserID, &item.Key, &kind, &item.Value, &item.ValidFrom, &validTo, &item.Source, &item.Confidence, &raw, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Kind = memory.Kind(kind)
		item.ValidFrom = item.ValidFrom.UTC()
		item.CreatedAt = item.CreatedAt.UTC()
		if validTo != nil {
			value := validTo.UTC()
			item.ValidTo = &value
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &item.Embedding); err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ForgetMemory(ctx context.Context, userID, key string) error {
	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx, `UPDATE memories SET deleted_at=$1,valid_to=COALESCE(valid_to,$1) WHERE user_id=$2 AND memory_key=$3 AND deleted_at IS NULL`, now, owner(userID), key)
	if err != nil {
		return err
	}
	return requireRowsChanged(tag.RowsAffected(), "memory key")
}

func (s *Store) ForgetMemoryMutation(ctx context.Context, request idempotency.Request, userID, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("memory key is required")
	}
	return runMutationMarker(ctx, s, request, "memory.forget", func(tx pgx.Tx) error {
		now := time.Now().UTC()
		tag, err := tx.Exec(ctx, `UPDATE memories SET deleted_at=$1,valid_to=COALESCE(valid_to,$1) WHERE user_id=$2 AND memory_key=$3 AND deleted_at IS NULL`, now, owner(userID), key)
		if err != nil {
			return err
		}
		return requireRowsChanged(tag.RowsAffected(), "memory key")
	})
}

func (s *Store) UpsertVector(ctx context.Context, userID string, memoryID int64, vector []float32) error {
	raw, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO memory_vectors(memory_id,user_id,embedding,updated_at) VALUES($1,$2,$3::jsonb,$4) ON CONFLICT(memory_id) DO UPDATE SET user_id=EXCLUDED.user_id,embedding=EXCLUDED.embedding,updated_at=EXCLUDED.updated_at`, memoryID, owner(userID), string(raw), time.Now().UTC())
	return err
}

func (s *Store) DeleteVector(ctx context.Context, userID string, memoryID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM memory_vectors WHERE memory_id=$1 AND user_id=$2`, memoryID, owner(userID))
	return err
}

func (s *Store) SearchVectors(ctx context.Context, userID string, query []float32, limit int) ([]memory.VectorHit, error) {
	if limit <= 0 || limit > 500 {
		limit = 80
	}
	rows, err := s.pool.Query(ctx, `SELECT memory_id,embedding FROM memory_vectors WHERE user_id=$1`, owner(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []memory.VectorHit
	for rows.Next() {
		var memoryID int64
		var raw []byte
		if err := rows.Scan(&memoryID, &raw); err != nil {
			return nil, err
		}
		var vector []float32
		if err := json.Unmarshal(raw, &vector); err != nil {
			continue
		}
		score := vectorCosine(query, vector)
		hits = append(hits, memory.VectorHit{ID: memoryID, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func vectorCosine(a, b []float32) float64 {
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

func (s *Store) GetPrivacyPolicy(ctx context.Context, userID string) (privacy.Policy, bool, error) {
	var policy privacy.Policy
	err := s.pool.QueryRow(ctx, `SELECT user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at FROM privacy_policies WHERE user_id=$1`, owner(userID)).Scan(&policy.UserID, &policy.SaveVoiceAudio, &policy.LongTermMemoryEnabled, &policy.ConversationRetentionDays, &policy.VoiceMemoRetentionDays, &policy.MemoryRetentionDays, &policy.UpdatedAt)
	if err == pgx.ErrNoRows {
		return policy, false, nil
	}
	if err != nil {
		return policy, false, err
	}
	policy.UpdatedAt = policy.UpdatedAt.UTC()
	return policy, true, nil
}

func (s *Store) SetPrivacyPolicy(ctx context.Context, policy privacy.Policy) error {
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO privacy_policies(user_id,save_voice_audio,long_term_memory_enabled,conversation_retention_days,voice_memo_retention_days,memory_retention_days,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(user_id) DO UPDATE SET save_voice_audio=EXCLUDED.save_voice_audio,long_term_memory_enabled=EXCLUDED.long_term_memory_enabled,conversation_retention_days=EXCLUDED.conversation_retention_days,voice_memo_retention_days=EXCLUDED.voice_memo_retention_days,memory_retention_days=EXCLUDED.memory_retention_days,updated_at=EXCLUDED.updated_at`, owner(policy.UserID), policy.SaveVoiceAudio, policy.LongTermMemoryEnabled, policy.ConversationRetentionDays, policy.VoiceMemoRetentionDays, policy.MemoryRetentionDays, policy.UpdatedAt.UTC())
	return err
}

func (s *Store) ApplyRetention(ctx context.Context, now time.Time) (privacy.RetentionReport, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id,conversation_retention_days,voice_memo_retention_days,memory_retention_days FROM privacy_policies WHERE conversation_retention_days>0 OR voice_memo_retention_days>0 OR memory_retention_days>0`)
	if err != nil {
		return privacy.RetentionReport{}, err
	}
	type policyRow struct {
		userID                      string
		conversationDays, voiceDays int
		memoryDays                  int
	}
	var policies []policyRow
	for rows.Next() {
		var policy policyRow
		if err := rows.Scan(&policy.userID, &policy.conversationDays, &policy.voiceDays, &policy.memoryDays); err != nil {
			rows.Close()
			return privacy.RetentionReport{}, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return privacy.RetentionReport{}, err
	}
	rows.Close()

	var report privacy.RetentionReport
	for _, policy := range policies {
		if policy.conversationDays > 0 {
			cutoff := now.AddDate(0, 0, -policy.conversationDays).UTC()
			tag, err := s.pool.Exec(ctx, `DELETE FROM conversation_messages WHERE user_id=$1 AND created_at<$2`, policy.userID, cutoff)
			if err != nil {
				return report, err
			}
			report.ConversationRows += int(tag.RowsAffected())
		}
		if policy.memoryDays > 0 {
			cutoff := now.AddDate(0, 0, -policy.memoryDays).UTC()
			tag, err := s.pool.Exec(ctx, `DELETE FROM memories WHERE user_id=$1 AND created_at<$2`, policy.userID, cutoff)
			if err != nil {
				return report, err
			}
			report.MemoryRows += int(tag.RowsAffected())
		}
		if policy.voiceDays > 0 {
			cutoff := now.AddDate(0, 0, -policy.voiceDays).UTC()
			voiceRows, err := s.pool.Query(ctx, `SELECT path FROM voice_memos WHERE user_id=$1 AND created_at<$2`, policy.userID, cutoff)
			if err != nil {
				return report, err
			}
			var paths []string
			for voiceRows.Next() {
				var path string
				if err := voiceRows.Scan(&path); err != nil {
					voiceRows.Close()
					return report, err
				}
				if path != "" {
					paths = append(paths, path)
				}
			}
			if err := voiceRows.Err(); err != nil {
				voiceRows.Close()
				return report, err
			}
			voiceRows.Close()
			tag, err := s.pool.Exec(ctx, `DELETE FROM voice_memos WHERE user_id=$1 AND created_at<$2`, policy.userID, cutoff)
			if err != nil {
				return report, err
			}
			report.VoiceMemoRows += int(tag.RowsAffected())
			report.OrphanPaths = append(report.OrphanPaths, paths...)
		}
	}
	return report, nil
}

func (s *Store) ReferencedVoiceMemoPaths(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT path FROM voice_memos WHERE path<>'' ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// Usage metering must never block the realtime/model path. Match the SQLite
// contract: record best-effort and expose DB errors only through the reader.
func (s *Store) RecordUsage(ctx context.Context, record usage.Record) {
	if record.PromptTokens < 0 || record.CompletionTokens < 0 || record.TotalTokens < 0 {
		return
	}
	_, _ = s.pool.Exec(ctx, `INSERT INTO llm_usage(user_id,device_id,provider,model,prompt_version,prompt_tokens,completion_tokens,total_tokens,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, owner(record.UserID), record.DeviceID, record.Provider, record.Model, record.PromptVersion, record.PromptTokens, record.CompletionTokens, record.TotalTokens, time.Now().UTC())
}

func (s *Store) TotalTokensSince(ctx context.Context, userID string, since time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(total_tokens),0) FROM llm_usage WHERE user_id=$1 AND created_at>=$2`, owner(userID), since.UTC()).Scan(&total)
	return total, err
}

var _ memory.Repository = (*Store)(nil)
var _ memory.DurableRepository = (*Store)(nil)
var _ memory.VectorStore = (*Store)(nil)
var _ privacy.Repository = (*Store)(nil)
var _ privacy.RecordingReferenceRepository = (*Store)(nil)
var _ usage.Meter = (*Store)(nil)
var _ usage.TotalReader = (*Store)(nil)
