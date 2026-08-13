package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/idempotency"
)

func (s *Store) ReplayVoiceMemoMutation(ctx context.Context, request idempotency.Request) (domain.VoiceMemo, bool, error) {
	if err := requireMutationOperation(request, "voice_memo.save"); err != nil {
		return domain.VoiceMemo{}, false, err
	}
	if err := s.migrateIdempotency(); err != nil {
		return domain.VoiceMemo{}, false, err
	}
	var storedHash, outcome string
	err := s.db.QueryRowContext(ctx, `SELECT request_hash,outcome_json FROM idempotency_records WHERE actor_id=? AND operation=? AND idempotency_key=?`, request.Actor, request.Operation, request.Key).Scan(&storedHash, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.VoiceMemo{}, false, nil
	}
	if err != nil {
		return domain.VoiceMemo{}, false, err
	}
	if !idempotency.EqualHash(storedHash, request.RequestHash) {
		return domain.VoiceMemo{}, false, idempotency.Conflict{Operation: request.Operation, Key: request.Key}
	}
	var memo domain.VoiceMemo
	if err := json.Unmarshal([]byte(outcome), &memo); err != nil {
		return domain.VoiceMemo{}, false, fmt.Errorf("decode committed voice memo outcome: %w", err)
	}
	return memo, true, nil
}

func (s *Store) CreateVoiceMemoMutation(ctx context.Context, request idempotency.Request, userID, deviceID, path, transcript string, durationMS int64) (domain.VoiceMemo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return domain.VoiceMemo{}, fmt.Errorf("voice memo path is required")
	}
	if durationMS < 0 {
		return domain.VoiceMemo{}, fmt.Errorf("voice memo duration must be non-negative")
	}
	return runMutationValue(ctx, s, request, "voice_memo.save", func(tx *sql.Tx) (domain.VoiceMemo, error) {
		created := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `INSERT INTO voice_memos(idempotency_key,user_id,device_id,path,transcript,duration_ms,created_at) VALUES(?,?,?,?,?,?,?)`, request.Key, owner(userID), strings.TrimSpace(deviceID), path, strings.TrimSpace(transcript), durationMS, created.Format(time.RFC3339Nano))
		if err != nil {
			return domain.VoiceMemo{}, err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return domain.VoiceMemo{}, err
		}
		return domain.VoiceMemo{ID: id, UserID: owner(userID), DeviceID: strings.TrimSpace(deviceID), Path: path, Transcript: strings.TrimSpace(transcript), DurationMS: durationMS, CreatedAt: created}, nil
	})
}

func (s *Store) DeleteVoiceMemoMutation(ctx context.Context, request idempotency.Request, userID string, id int64) (domain.VoiceMemo, error) {
	if id < 1 {
		return domain.VoiceMemo{}, fmt.Errorf("voice memo id is required")
	}
	return runMutationValue(ctx, s, request, "voice_memo.delete", func(tx *sql.Tx) (domain.VoiceMemo, error) {
		var memo domain.VoiceMemo
		var created string
		err := tx.QueryRowContext(ctx, `SELECT id,user_id,device_id,path,transcript,duration_ms,created_at FROM voice_memos WHERE id=? AND user_id=?`, id, owner(userID)).Scan(&memo.ID, &memo.UserID, &memo.DeviceID, &memo.Path, &memo.Transcript, &memo.DurationMS, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VoiceMemo{}, fmt.Errorf("voice memo not found")
		}
		if err != nil {
			return domain.VoiceMemo{}, err
		}
		memo.CreatedAt, _ = parseStoredTime(created)
		result, err := tx.ExecContext(ctx, `DELETE FROM voice_memos WHERE id=? AND user_id=?`, id, owner(userID))
		if err != nil {
			return domain.VoiceMemo{}, err
		}
		if err := requireChanged(result, nil, "voice memo"); err != nil {
			return domain.VoiceMemo{}, err
		}
		return memo, nil
	})
}

func (s *Store) ClearConversationMutation(ctx context.Context, request idempotency.Request, userID, threadID string) (bool, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		threadID = "default"
	}
	if err := requireMutationOperation(request, "conversation.clear"); err != nil {
		return false, err
	}
	outcome, err := s.runIdempotentMutation(ctx, request, func(tx *sql.Tx) (any, error) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_messages WHERE user_id=? AND thread_id=?`, owner(userID), threadID); err != nil {
			return nil, err
		}
		return map[string]any{"cleared": true, "thread_id": threadID}, nil
	})
	if err != nil {
		return false, err
	}
	return outcome.Replayed, nil
}
