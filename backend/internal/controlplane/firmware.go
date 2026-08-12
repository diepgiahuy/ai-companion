package controlplane

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type FirmwareRepository interface {
	PutFirmware(context.Context, FirmwareManifest) error
	LatestFirmware(context.Context, string, string, int, int, time.Time) (FirmwareManifest, bool, error)
}
type FirmwareService struct {
	repo             FirmwareRepository
	publicKey        ed25519.PublicKey
	requireSignature bool
	now              func() time.Time
}

func NewFirmware(repo FirmwareRepository, publicKey ed25519.PublicKey, requireSignature bool) *FirmwareService {
	return &FirmwareService{repo: repo, publicKey: publicKey, requireSignature: requireSignature, now: time.Now}
}
func (s *FirmwareService) Publish(ctx context.Context, m FirmwareManifest) error {
	if e := m.Validate(s.now()); e != nil {
		return e
	}
	if s.requireSignature {
		if len(s.publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("OTA public key missing")
		}
		if !m.Verify(s.publicKey) {
			return fmt.Errorf("firmware manifest signature invalid")
		}
	}
	return s.repo.PutFirmware(ctx, m)
}
func (s *FirmwareService) Latest(ctx context.Context, channel, board string, protocol, currentSecurity int, currentMetadata int64) (FirmwareManifest, bool, error) {
	m, ok, e := s.repo.LatestFirmware(ctx, channel, board, protocol, currentSecurity, s.now())
	if e != nil || !ok {
		return m, ok, e
	}
	if m.MetadataVersion <= currentMetadata {
		return FirmwareManifest{}, false, nil
	}
	if e := m.Validate(s.now()); e != nil {
		return FirmwareManifest{}, false, e
	}
	if s.requireSignature && !m.Verify(s.publicKey) {
		return FirmwareManifest{}, false, fmt.Errorf("stored firmware signature invalid")
	}
	return m, true, nil
}
func (m FirmwareManifest) signingBytes() []byte {
	type signed struct {
		Version         string    `json:"version"`
		Channel         string    `json:"channel"`
		Board           string    `json:"board"`
		ProtocolMin     int       `json:"protocol_min"`
		SecurityVersion int       `json:"security_version"`
		URL             string    `json:"url"`
		SHA256          string    `json:"sha256"`
		Size            int64     `json:"size"`
		ExpiresAt       time.Time `json:"expires_at"`
		MetadataVersion int64     `json:"metadata_version"`
	}
	b, _ := json.Marshal(signed{m.Version, m.Channel, m.Board, m.ProtocolMin, m.SecurityVersion, m.URL, m.SHA256, m.Size, m.ExpiresAt.UTC(), m.MetadataVersion})
	return b
}
func (m *FirmwareManifest) Sign(key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid Ed25519 private key")
	}
	m.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, m.signingBytes()))
	return nil
}
func (m FirmwareManifest) Verify(key ed25519.PublicKey) bool {
	if len(key) != ed25519.PublicKeySize || m.Signature == "" {
		return false
	}
	sig, e := base64.RawURLEncoding.DecodeString(m.Signature)
	return e == nil && ed25519.Verify(key, m.signingBytes(), sig)
}
func DecodeEd25519PublicKey(v string) (ed25519.PublicKey, error) {
	if v == "" {
		return nil, nil
	}
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil {
		return nil, e
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("OTA public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}
