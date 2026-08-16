package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/events"
	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"
	"companion-server/internal/voicemail"

	"github.com/jackc/pgx/v5"
)

const voiceMailColumns = `id,relationship_id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,playback_id,lease_expires_at,expires_at,created_at,updated_at`

func (s *Store) ListRecipients(ctx context.Context, senderUserID, senderDeviceID string) ([]pairing.RecipientDescriptor, error) {
	return s.ListAuthorizedRecipients(ctx, pairing.Participant{UserID: senderUserID, DeviceID: senderDeviceID})
}

func (s *Store) CreateUpload(ctx context.Context, request idempotency.Request, create voicemail.Create, now time.Time) (voicemail.Item, error) {
	return runMutationValue(ctx, s, request, "voice_mail.create", func(tx pgx.Tx) (voicemail.Item, error) {
		relID := strings.TrimSpace(create.RelationshipID)
		senderUser := owner(create.SenderUserID)
		senderDevice := strings.TrimSpace(create.SenderDeviceID)

		var devA, userA, devB, userB string
		err := tx.QueryRow(ctx, `
			SELECT device_a_id, user_a_id, device_b_id, user_b_id
			FROM device_relationships
			WHERE relationship_id=$1 AND revoked_at IS NULL FOR SHARE`, relID).Scan(&devA, &userA, &devB, &userB)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return voicemail.Item{}, voicemail.ErrRelationshipNotFound
			}
			return voicemail.Item{}, fmt.Errorf("verify relationship: %w", err)
		}

		var recipientUser, recipientDevice string
		if userA == senderUser && devA == senderDevice {
			recipientUser, recipientDevice = userB, devB
		} else if userB == senderUser && devB == senderDevice {
			recipientUser, recipientDevice = userA, devA
		} else {
			return voicemail.Item{}, voicemail.ErrUnauthorized
		}

		if err := requireVoiceMailPolicy(ctx, tx, senderUser, create.Policy); err != nil {
			return voicemail.Item{}, err
		}
		if err := requireVoiceMailPolicy(ctx, tx, recipientUser, create.Policy); err != nil {
			return voicemail.Item{}, err
		}

		item := voicemail.Item{
			RelationshipID:    relID,
			SenderUserID:      senderUser,
			SenderDeviceID:    senderDevice,
			RecipientUserID:   recipientUser,
			RecipientDeviceID: recipientDevice,
			MediaFormat:       voicemail.MediaFormat,
			DurationMS:        create.DurationMS,
			SizeBytes:         create.SizeBytes,
			ChecksumSHA256:    strings.ToLower(create.ChecksumSHA256),
			Policy:            create.Policy,
			State:             voicemail.PendingUpload,
			ExpiresAt:         create.ExpiresAt.UTC(),
			CreatedAt:         now.UTC(),
			UpdatedAt:         now.UTC(),
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO voice_mail_items(id,relationship_id,sender_user_id,sender_device_id,recipient_user_id,recipient_device_id,object_key,media_format,duration_ms,size_bytes,checksum_sha256,policy,state,expires_at,created_at,updated_at)
			VALUES(gen_random_uuid()::text,$1,$2,$3,$4,$5,gen_random_uuid()::text,$6,$7,$8,$9,$10,$11,$12,$13,$13)
			RETURNING id,object_key`, item.RelationshipID, item.SenderUserID, item.SenderDeviceID, item.RecipientUserID, item.RecipientDeviceID,
			item.MediaFormat, item.DurationMS, item.SizeBytes, item.ChecksumSHA256, item.Policy, item.State, item.ExpiresAt, item.CreatedAt,
		).Scan(&item.ID, &item.ObjectKey)
		return item, err
	})
}

func requireVoiceMailPolicy(ctx context.Context, tx pgx.Tx, userID string, expected voicemail.Policy) error {
	var policy string
	err := tx.QueryRow(ctx, `SELECT voice_mail_policy FROM privacy_policies WHERE user_id=$1`, owner(userID)).Scan(&policy)
	if err == pgx.ErrNoRows || policy == string(voicemail.Disabled) {
		return fmt.Errorf("voice mail is disabled")
	}
	if err != nil {
		return err
	}
	if policy != string(expected) {
		return fmt.Errorf("voice mail policy does not match recipient policy")
	}
	return nil
}

func (s *Store) ItemForSender(ctx context.Context, senderUserID, senderDeviceID, id string) (voicemail.Item, bool, error) {
	item, err := scanVoiceMail(s.pool.QueryRow(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items WHERE id=$1 AND sender_user_id=$2 AND sender_device_id=$3`, strings.TrimSpace(id), owner(senderUserID), strings.TrimSpace(senderDeviceID)))
	if err == pgx.ErrNoRows {
		return voicemail.Item{}, false, nil
	}
	return item, err == nil, err
}

func (s *Store) CompleteUpload(ctx context.Context, request idempotency.Request, senderUserID, senderDeviceID, id string, now time.Time) (voicemail.Item, error) {
	item, err := runMutationValue(ctx, s, request, "voice_mail.complete", func(tx pgx.Tx) (voicemail.Item, error) {
		item, err := scanVoiceMail(tx.QueryRow(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items WHERE id=$1 AND sender_user_id=$2 AND sender_device_id=$3 FOR UPDATE`, id, owner(senderUserID), strings.TrimSpace(senderDeviceID)))
		if err == pgx.ErrNoRows {
			return voicemail.Item{}, voicemail.ErrItemNotFound
		}
		if err != nil {
			return voicemail.Item{}, err
		}
		if item.State != voicemail.PendingUpload {
			return voicemail.Item{}, fmt.Errorf("voice mail upload is not pending")
		}
		if !item.ExpiresAt.After(now) {
			return voicemail.Item{}, fmt.Errorf("voice mail upload expired")
		}

		// Re-verify the exact relationship generation while holding a shared
		// row lock. Revocation takes an UPDATE lock, so Complete vs Revoke has
		// only serialized outcomes: either this commit wins while R is active,
		// or revocation wins and this pending item is durably rejected.
		var revokedAt *time.Time
		var devA, userA, devB, userB string
		err = tx.QueryRow(ctx, `
			SELECT device_a_id, user_a_id, device_b_id, user_b_id, revoked_at
			FROM device_relationships
			WHERE relationship_id=$1 FOR SHARE`, item.RelationshipID).Scan(&devA, &userA, &devB, &userB, &revokedAt)
		if errors.Is(err, pgx.ErrNoRows) || revokedAt != nil {
			item.State, item.UpdatedAt = voicemail.Rejected, now.UTC()
			if _, updateErr := tx.Exec(ctx, `UPDATE voice_mail_items SET state='rejected',updated_at=$1 WHERE id=$2`, item.UpdatedAt, item.ID); updateErr != nil {
				return voicemail.Item{}, updateErr
			}
			// Returning a value lets RunIdempotent commit both the terminal state
			// and replayable outcome. The repository maps the committed state to
			// ErrRelationshipRevoked only after the transaction has committed.
			return item, nil
		}
		if err != nil {
			return voicemail.Item{}, fmt.Errorf("revalidate relationship: %w", err)
		}

		senderUser := owner(senderUserID)
		senderDevice := strings.TrimSpace(senderDeviceID)
		validGeneration := (userA == senderUser && devA == senderDevice && userB == item.RecipientUserID && devB == item.RecipientDeviceID) ||
			(userB == senderUser && devB == senderDevice && userA == item.RecipientUserID && devA == item.RecipientDeviceID)
		if !validGeneration {
			return voicemail.Item{}, voicemail.ErrUnauthorized
		}

		item.State, item.UpdatedAt = voicemail.Unread, now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE voice_mail_items SET state='unread',updated_at=$1 WHERE id=$2`, item.UpdatedAt, item.ID); err != nil {
			return voicemail.Item{}, err
		}
		if err := enqueueVoiceMailEvent(ctx, tx, item, "voice_mail.available", now); err != nil {
			return voicemail.Item{}, err
		}
		return item, nil
	})
	if err != nil {
		return voicemail.Item{}, err
	}
	if item.State == voicemail.Rejected {
		return item, voicemail.ErrRelationshipRevoked
	}
	return item, nil
}

func (s *Store) ListUnread(ctx context.Context, recipientUserID, deviceID string, now time.Time, limit int) ([]voicemail.Item, error) {
	limit = boundedLimit(limit)
	rows, err := s.pool.Query(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items
		WHERE recipient_user_id=$1 AND state='unread' AND expires_at>$2 AND (recipient_device_id='' OR recipient_device_id=$3)
		ORDER BY created_at,id LIMIT $4`, owner(recipientUserID), now.UTC(), strings.TrimSpace(deviceID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []voicemail.Item
	for rows.Next() {
		item, err := scanVoiceMail(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ClaimVoiceMail(ctx context.Context, request idempotency.Request, recipientUserID, deviceID, id, playbackID string, now, leaseExpiresAt time.Time) (voicemail.Item, error) {
	if strings.TrimSpace(playbackID) == "" {
		return voicemail.Item{}, fmt.Errorf("playback_id is required")
	}
	return runMutationValue(ctx, s, request, "voice_mail.claim", func(tx pgx.Tx) (voicemail.Item, error) {
		query := `SELECT ` + voiceMailColumns + ` FROM voice_mail_items WHERE recipient_user_id=$1 AND expires_at>$2 AND (recipient_device_id='' OR recipient_device_id=$3)`
		args := []any{owner(recipientUserID), now.UTC(), strings.TrimSpace(deviceID)}
		if strings.TrimSpace(id) == "" {
			query += ` AND (state='unread' OR (state='claimed' AND lease_expires_at<=$2)) ORDER BY created_at,id LIMIT 1 FOR UPDATE`
		} else {
			query += ` AND id=$4 FOR UPDATE`
			args = append(args, id)
		}
		item, err := scanVoiceMail(tx.QueryRow(ctx, query, args...))
		if err == pgx.ErrNoRows {
			return voicemail.Item{}, fmt.Errorf("no unread voice mail")
		}
		if err != nil {
			return voicemail.Item{}, err
		}
		if item.State == voicemail.Claimed && item.LeaseExpiresAt != nil && item.LeaseExpiresAt.After(now) {
			return voicemail.Item{}, fmt.Errorf("voice mail is already claimed")
		}
		if item.State != voicemail.Unread && item.State != voicemail.Claimed {
			return voicemail.Item{}, fmt.Errorf("voice mail is not unread")
		}
		lease := leaseExpiresAt.UTC()
		item.State, item.PlaybackID, item.LeaseExpiresAt, item.UpdatedAt = voicemail.Claimed, playbackID, &lease, now.UTC()
		_, err = tx.Exec(ctx, `UPDATE voice_mail_items SET state='claimed',playback_id=$1,lease_expires_at=$2,updated_at=$3 WHERE id=$4`, playbackID, lease, now.UTC(), item.ID)
		return item, err
	})
}

func (s *Store) CompleteVoiceMailPlayback(ctx context.Context, request idempotency.Request, recipientUserID, recipientDeviceID, id, playbackID string, succeeded bool, now time.Time) (voicemail.Item, error) {
	return runMutationValue(ctx, s, request, "voice_mail.playback", func(tx pgx.Tx) (voicemail.Item, error) {
		item, err := scanVoiceMail(tx.QueryRow(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items WHERE id=$1 AND recipient_user_id=$2 AND (recipient_device_id='' OR recipient_device_id=$3) FOR UPDATE`, id, owner(recipientUserID), strings.TrimSpace(recipientDeviceID)))
		if err == pgx.ErrNoRows {
			return voicemail.Item{}, fmt.Errorf("voice mail not found")
		}
		if err != nil {
			return voicemail.Item{}, err
		}
		if item.State != voicemail.Claimed || item.PlaybackID != playbackID || item.LeaseExpiresAt == nil || !item.LeaseExpiresAt.After(now) {
			return voicemail.Item{}, fmt.Errorf("voice mail claim is invalid or expired")
		}
		next := voicemail.Unread
		if succeeded && item.Policy == voicemail.Ephemeral {
			next = voicemail.DeletePending
		}
		item.State, item.PlaybackID, item.LeaseExpiresAt, item.UpdatedAt = next, "", nil, now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE voice_mail_items SET state=$1,playback_id='',lease_expires_at=NULL,updated_at=$2 WHERE id=$3`, next, now.UTC(), item.ID); err != nil {
			return voicemail.Item{}, err
		}
		if succeeded {
			eventType := "voice_mail.consumed"
			if item.State == voicemail.Unread {
				eventType = "voice_mail.available"
			}
			if err := enqueueVoiceMailEvent(ctx, tx, item, eventType, now); err != nil {
				return voicemail.Item{}, err
			}
		}
		return item, nil
	})
}

func (s *Store) ItemForPlayback(ctx context.Context, recipientUserID, recipientDeviceID, id, playbackID string, now time.Time) (voicemail.Item, bool, error) {
	item, err := scanVoiceMail(s.pool.QueryRow(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items WHERE id=$1 AND recipient_user_id=$2 AND (recipient_device_id='' OR recipient_device_id=$3) AND state='claimed' AND playback_id=$4 AND lease_expires_at>$5`, id, owner(recipientUserID), strings.TrimSpace(recipientDeviceID), playbackID, now.UTC()))
	if err == pgx.ErrNoRows {
		return voicemail.Item{}, false, nil
	}
	return item, err == nil, err
}

func (s *Store) RequestDelete(ctx context.Context, request idempotency.Request, ownerUserID, id string, now time.Time) (voicemail.Item, error) {
	return runMutationValue(ctx, s, request, "voice_mail.delete", func(tx pgx.Tx) (voicemail.Item, error) {
		item, err := scanVoiceMail(tx.QueryRow(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items WHERE id=$1 AND (sender_user_id=$2 OR recipient_user_id=$2) FOR UPDATE`, id, owner(ownerUserID)))
		if err == pgx.ErrNoRows {
			return voicemail.Item{}, fmt.Errorf("voice mail not found")
		}
		if err != nil {
			return voicemail.Item{}, err
		}
		if item.State == voicemail.Deleted {
			return item, nil
		}
		item.State, item.PlaybackID, item.LeaseExpiresAt, item.UpdatedAt = voicemail.DeletePending, "", nil, now.UTC()
		_, err = tx.Exec(ctx, `UPDATE voice_mail_items SET state='delete_pending',playback_id='',lease_expires_at=NULL,updated_at=$1 WHERE id=$2`, now.UTC(), item.ID)
		return item, err
	})
}

func (s *Store) MarkDeleted(ctx context.Context, id string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE voice_mail_items SET state='deleted',updated_at=$1 WHERE id=$2 AND state IN ('delete_pending','deleted','rejected')`, now.UTC(), id)
	return err
}

func (s *Store) ClaimCleanup(ctx context.Context, now time.Time, limit int) ([]voicemail.Item, error) {
	limit = boundedLimit(limit)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+voiceMailColumns+` FROM voice_mail_items
		WHERE state IN ('delete_pending','rejected') OR (state IN ('pending_upload','unread','claimed') AND expires_at<=$1)
		ORDER BY expires_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	var items []voicemail.Item
	for rows.Next() {
		item, err := scanVoiceMail(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range items {
		if items[i].State == voicemail.Unread || items[i].State == voicemail.Claimed {
			if err := enqueueVoiceMailEvent(ctx, tx, items[i], "voice_mail.expired", now); err != nil {
				return nil, err
			}
		}
		items[i].State, items[i].PlaybackID, items[i].LeaseExpiresAt, items[i].UpdatedAt = voicemail.DeletePending, "", nil, now.UTC()
		if _, err := tx.Exec(ctx, `UPDATE voice_mail_items SET state='delete_pending',playback_id='',lease_expires_at=NULL,updated_at=$1 WHERE id=$2`, now.UTC(), items[i].ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func enqueueVoiceMailEvent(ctx context.Context, tx pgx.Tx, item voicemail.Item, eventType string, now time.Time) error {
	data, err := json.Marshal(map[string]any{"voice_mail_id": item.ID, "recipient_device_id": item.RecipientDeviceID, "from_device_id": item.SenderDeviceID, "media_format": item.MediaFormat, "duration_ms": item.DurationMS, "size_bytes": item.SizeBytes, "checksum_sha256": item.ChecksumSHA256, "expires_at": item.ExpiresAt, "policy": item.Policy, "state": item.State})
	if err != nil {
		return err
	}
	eventID := "voice-mail-" + item.ID + "-" + strings.TrimPrefix(eventType, "voice_mail.") + "-" + strconv.FormatInt(now.UnixNano(), 10)
	event := events.Event{ID: eventID, Source: "/companion/voice-mail", Type: eventType, Subject: "voice-mail/" + item.ID, UserID: item.RecipientUserID, Data: data, Time: now.UTC()}
	_, err = tx.Exec(ctx, `INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,status,attempts,next_attempt_at,last_error) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,'pending',0,$7,'') ON CONFLICT(event_id) DO NOTHING`, event.ID, event.Source, event.Type, event.Subject, event.UserID, string(event.Data), event.Time)
	return err
}

type voiceMailScanner interface{ Scan(...any) error }

func scanVoiceMail(row voiceMailScanner) (voicemail.Item, error) {
	var item voicemail.Item
	var relID *string
	err := row.Scan(&item.ID, &relID, &item.SenderUserID, &item.SenderDeviceID, &item.RecipientUserID, &item.RecipientDeviceID, &item.ObjectKey, &item.MediaFormat, &item.DurationMS, &item.SizeBytes, &item.ChecksumSHA256, &item.Policy, &item.State, &item.PlaybackID, &item.LeaseExpiresAt, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		if relID != nil {
			item.RelationshipID = *relID
		}
		item.ExpiresAt = item.ExpiresAt.UTC()
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = item.UpdatedAt.UTC()
		if item.LeaseExpiresAt != nil {
			t := item.LeaseExpiresAt.UTC()
			item.LeaseExpiresAt = &t
		}
	}
	return item, err
}

var _ voicemail.Repository = (*Store)(nil)
