package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/controlplane"
	"companion-server/internal/idempotency"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) ClaimDevice(ctx context.Context, mutation controlplane.DeviceClaimMutation) (controlplane.DeviceClaimOutcome, error) {
	mutation.UserID = strings.TrimSpace(mutation.UserID)
	mutation.DeviceID = strings.TrimSpace(mutation.DeviceID)
	mutation.DeliveryID = strings.TrimSpace(mutation.DeliveryID)
	mutation.CredentialHash = strings.TrimSpace(mutation.CredentialHash)
	if mutation.UserID == "" || mutation.DeviceID == "" || mutation.DeliveryID == "" ||
		len(mutation.CredentialHash) != 64 || len(mutation.CredentialCiphertext) == 0 ||
		len(mutation.CredentialNonce) == 0 || !mutation.ExpiresAt.After(time.Now().UTC()) {
		return controlplane.DeviceClaimOutcome{}, fmt.Errorf("invalid device claim mutation")
	}
	request := idempotency.Request{
		Actor:       mutation.UserID,
		Operation:   "device.claim",
		Key:         mutation.IdempotencyKey,
		RequestHash: mutation.RequestHash,
	}
	outcome, err := RunIdempotent(ctx, s.pool, request, func(tx pgx.Tx) (any, error) {
		var owner string
		err := tx.QueryRow(ctx, `SELECT user_id FROM device_credentials WHERE device_id = $1 FOR UPDATE`, mutation.DeviceID).Scan(&owner)
		switch {
		case err == nil:
			return nil, controlplane.ErrDeviceAlreadyClaimed
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, fmt.Errorf("check existing device claim: %w", err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO device_credentials(
				device_id,user_id,tenant_id,plan,token_sha256,status,created_at,rotated_at
			) VALUES ($1,$2,$3,$4,$5,'active',now(),now())`,
			mutation.DeviceID,
			mutation.UserID,
			strings.TrimSpace(mutation.TenantID),
			strings.TrimSpace(mutation.Plan),
			mutation.CredentialHash,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "device_credentials_pkey" {
				return nil, controlplane.ErrDeviceAlreadyClaimed
			}
			return nil, fmt.Errorf("insert claimed device credential: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO device_claim_deliveries(
				delivery_id,device_id,user_id,credential_ciphertext,credential_nonce,expires_at,created_at
			) VALUES ($1,$2,$3,$4,$5,$6,now())`,
			mutation.DeliveryID,
			mutation.DeviceID,
			mutation.UserID,
			mutation.CredentialCiphertext,
			mutation.CredentialNonce,
			mutation.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("insert device claim delivery: %w", err)
		}
		return controlplane.DeviceClaimOutcome{
			DeliveryID: mutation.DeliveryID,
			DeviceID:   mutation.DeviceID,
		}, nil
	})
	if err != nil {
		return controlplane.DeviceClaimOutcome{}, err
	}
	var decoded controlplane.DeviceClaimOutcome
	if err := json.Unmarshal([]byte(outcome.JSON), &decoded); err != nil {
		return controlplane.DeviceClaimOutcome{}, fmt.Errorf("decode device claim outcome: %w", err)
	}
	decoded.Replayed = outcome.Replayed
	return decoded, nil
}

func (s *Store) DeviceClaimDelivery(ctx context.Context, userID, deliveryID string) (controlplane.DeviceClaimDelivery, error) {
	var delivery controlplane.DeviceClaimDelivery
	var completedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT delivery_id,device_id,user_id,credential_ciphertext,credential_nonce,expires_at,completed_at
		FROM device_claim_deliveries
		WHERE delivery_id=$1 AND user_id=$2`,
		strings.TrimSpace(deliveryID),
		strings.TrimSpace(userID),
	).Scan(
		&delivery.DeliveryID,
		&delivery.DeviceID,
		&delivery.UserID,
		&delivery.CredentialCiphertext,
		&delivery.CredentialNonce,
		&delivery.ExpiresAt,
		&completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return controlplane.DeviceClaimDelivery{}, controlplane.ErrClaimDeliveryUnavailable
	}
	if err != nil {
		return controlplane.DeviceClaimDelivery{}, fmt.Errorf("load device claim delivery: %w", err)
	}
	if completedAt != nil || !delivery.ExpiresAt.After(time.Now().UTC()) ||
		len(delivery.CredentialCiphertext) == 0 || len(delivery.CredentialNonce) == 0 {
		return controlplane.DeviceClaimDelivery{}, controlplane.ErrClaimDeliveryUnavailable
	}
	return delivery, nil
}
