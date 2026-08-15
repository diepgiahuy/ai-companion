package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"

	"github.com/jackc/pgx/v5"
)

func (s *Store) CreatePairingSession(ctx context.Context, mutation pairing.CreateMutation) (pairing.Session, bool, error) {
	request := idempotency.Request{
		Actor: mutation.Initiator.UserID, Operation: "pairing.session.create",
		Key: mutation.IdempotencyKey, RequestHash: mutation.RequestHash,
	}
	outcome, err := RunIdempotent(ctx, s.pool, request, func(tx pgx.Tx) (any, error) {
		var initiatorOwner, peerOwner string
		if err := tx.QueryRow(ctx, `SELECT user_id FROM device_credentials WHERE device_id=$1 AND status='active'`, mutation.Initiator.DeviceID).Scan(&initiatorOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) { return nil, pairing.ErrUnauthorized }
			return nil, fmt.Errorf("load pairing initiator: %w", err)
		}
		if initiatorOwner != mutation.Initiator.UserID {
			return nil, pairing.ErrUnauthorized
		}
		if err := tx.QueryRow(ctx, `SELECT user_id FROM device_credentials WHERE device_id=$1 AND status='active'`, mutation.Peer.DeviceID).Scan(&peerOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) { return nil, pairing.ErrDeviceUnavailable }
			return nil, fmt.Errorf("load pairing peer: %w", err)
		}
		mutation.Peer.UserID = peerOwner
		_, err := tx.Exec(ctx, `
			INSERT INTO pairing_sessions(
				session_id,initiator_user_id,initiator_device_id,peer_user_id,peer_device_id,
				proximity_evidence_id,initiator_nonce,peer_nonce,expires_at,state,created_at,updated_at
			) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',now(),now())`,
			mutation.ID, mutation.Initiator.UserID, mutation.Initiator.DeviceID,
			mutation.Peer.UserID, mutation.Peer.DeviceID, mutation.ProximityEvidenceID,
			mutation.InitiatorNonce, mutation.PeerNonce, mutation.ExpiresAt,
		)
		if err != nil { return nil, fmt.Errorf("insert pairing session: %w", err) }
		return mutation.Session, nil
	})
	if err != nil { return pairing.Session{}, false, err }
	var session pairing.Session
	if err := json.Unmarshal([]byte(outcome.JSON), &session); err != nil {
		return pairing.Session{}, false, fmt.Errorf("decode pairing create outcome: %w", err)
	}
	return session, outcome.Replayed, nil
}

func (s *Store) ConfirmPairingSession(ctx context.Context, mutation pairing.ConfirmMutation) (pairing.ConfirmationOutcome, error) {
	request := idempotency.Request{
		Actor: mutation.Participant.UserID, Operation: "pairing.session.confirm",
		Key: mutation.IdempotencyKey, RequestHash: mutation.RequestHash,
	}
	outcome, err := RunIdempotent(ctx, s.pool, request, func(tx pgx.Tx) (any, error) {
		var session pairing.Session
		var initiatorConfirmed, peerConfirmed *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT session_id,initiator_user_id,initiator_device_id,peer_user_id,peer_device_id,
				proximity_evidence_id,initiator_nonce,peer_nonce,initiator_confirmed_at,peer_confirmed_at,
				expires_at,COALESCE(relationship_id,''),state
			FROM pairing_sessions WHERE session_id=$1 FOR UPDATE`, mutation.SessionID).Scan(
			&session.ID, &session.Initiator.UserID, &session.Initiator.DeviceID,
			&session.Peer.UserID, &session.Peer.DeviceID, &session.ProximityEvidenceID,
			&session.InitiatorNonce, &session.PeerNonce, &initiatorConfirmed, &peerConfirmed,
			&session.ExpiresAt, &session.RelationshipID, &session.State,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) { return nil, pairing.ErrInvalidConfirmation }
			return nil, fmt.Errorf("load pairing session: %w", err)
		}
		session.InitiatorConfirmedAt = initiatorConfirmed
		session.PeerConfirmedAt = peerConfirmed
		if session.State != "pending" { return nil, pairing.ErrSessionClosed }
		if !session.ExpiresAt.After(mutation.ConfirmedAt) { return nil, pairing.ErrSessionExpired }

		isInitiator := mutation.Participant == session.Initiator
		isPeer := mutation.Participant == session.Peer
		if !isInitiator && !isPeer { return nil, pairing.ErrUnauthorized }
		if (isInitiator && mutation.Nonce != session.InitiatorNonce) || (isPeer && mutation.Nonce != session.PeerNonce) {
			return nil, pairing.ErrInvalidConfirmation
		}
		if (isInitiator && initiatorConfirmed != nil) || (isPeer && peerConfirmed != nil) {
			return nil, pairing.ErrInvalidConfirmation
		}
		if isInitiator {
			initiatorConfirmed = &mutation.ConfirmedAt
			if _, err := tx.Exec(ctx, `UPDATE pairing_sessions SET initiator_confirmed_at=$2,updated_at=now() WHERE session_id=$1`, session.ID, mutation.ConfirmedAt); err != nil {
				return nil, fmt.Errorf("record pairing confirmation: %w", err)
			}
		} else {
			peerConfirmed = &mutation.ConfirmedAt
			if _, err := tx.Exec(ctx, `UPDATE pairing_sessions SET peer_confirmed_at=$2,updated_at=now() WHERE session_id=$1`, session.ID, mutation.ConfirmedAt); err != nil {
				return nil, fmt.Errorf("record pairing confirmation: %w", err)
			}
		}
		session.InitiatorConfirmedAt = initiatorConfirmed
		session.PeerConfirmedAt = peerConfirmed
		if initiatorConfirmed == nil || peerConfirmed == nil {
			return pairing.ConfirmationOutcome{Session: session, Completed: false}, nil
		}

		deviceA, userA := session.Initiator.DeviceID, session.Initiator.UserID
		deviceB, userB := session.Peer.DeviceID, session.Peer.UserID
		if deviceB < deviceA {
			deviceA, deviceB = deviceB, deviceA
			userA, userB = userB, userA
		}
		relationshipID := mutation.RelationshipID
		inserted := false
		if err := tx.QueryRow(ctx, `
			INSERT INTO device_relationships(relationship_id,device_a_id,device_b_id,user_a_id,user_b_id,created_at)
			VALUES($1,$2,$3,$4,$5,now())
			ON CONFLICT(device_a_id,device_b_id) DO NOTHING
			RETURNING relationship_id`, relationshipID, deviceA, deviceB, userA, userB).Scan(&relationshipID); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) { return nil, fmt.Errorf("insert device relationship: %w", err) }
			if err := tx.QueryRow(ctx, `SELECT relationship_id FROM device_relationships WHERE device_a_id=$1 AND device_b_id=$2`, deviceA, deviceB).Scan(&relationshipID); err != nil {
				return nil, fmt.Errorf("load existing device relationship: %w", err)
			}
		} else {
			inserted = true
		}
		if _, err := tx.Exec(ctx, `UPDATE pairing_sessions SET state='paired',relationship_id=$2,updated_at=now() WHERE session_id=$1`, session.ID, relationshipID); err != nil {
			return nil, fmt.Errorf("complete pairing session: %w", err)
		}
		if inserted {
			data, _ := json.Marshal(map[string]string{"relationship_id": relationshipID, "device_a_id": deviceA, "device_b_id": deviceB})
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox(event_id,source,event_type,subject,user_id,data_json,occurred_at,next_attempt_at)
				VALUES(gen_random_uuid()::text,'/companion/relationship','pairing.relationship.created',$1,$2,$3::jsonb,clock_timestamp(),clock_timestamp())`,
				"relationship/"+relationshipID, userA, string(data)); err != nil {
				return nil, fmt.Errorf("enqueue pairing relationship event: %w", err)
			}
		}
		session.State = "paired"
		session.RelationshipID = relationshipID
		return pairing.ConfirmationOutcome{Session: session, Completed: true, RelationshipID: relationshipID}, nil
	})
	if err != nil { return pairing.ConfirmationOutcome{}, err }
	var decoded pairing.ConfirmationOutcome
	if err := json.Unmarshal([]byte(outcome.JSON), &decoded); err != nil {
		return pairing.ConfirmationOutcome{}, fmt.Errorf("decode pairing confirmation outcome: %w", err)
	}
	decoded.Replayed = outcome.Replayed
	return decoded, nil
}

func canonicalPair(a, b string) (string, string) {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if b < a { return b, a }
	return a, b
}
