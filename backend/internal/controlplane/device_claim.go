package controlplane

import (
	"context"
	"errors"
	"time"
)

var (
	ErrDeviceAlreadyClaimed      = errors.New("device is already claimed")
	ErrClaimDeliveryUnavailable = errors.New("device claim delivery is unavailable")
)

type DeviceClaimMutation struct {
	UserID               string
	DeviceID             string
	TenantID             string
	Plan                 string
	CredentialHash       string
	DeliveryID           string
	CredentialCiphertext []byte
	CredentialNonce      []byte
	ExpiresAt            time.Time
	IdempotencyKey       string
	RequestHash          string
}

type DeviceClaimOutcome struct {
	DeliveryID string `json:"delivery_id"`
	DeviceID   string `json:"device_id"`
	Replayed   bool   `json:"-"`
}

type DeviceClaimDelivery struct {
	DeliveryID           string
	DeviceID             string
	UserID               string
	CredentialCiphertext []byte
	CredentialNonce      []byte
	ExpiresAt            time.Time
}

type DeviceClaimRepository interface {
	ClaimDevice(context.Context, DeviceClaimMutation) (DeviceClaimOutcome, error)
	DeviceClaimDelivery(context.Context, string, string) (DeviceClaimDelivery, error)
}
