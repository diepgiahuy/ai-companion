package server

import (
	"fmt"
	"strings"

	"companion-server/internal/protocol"
)

type pairingCreateRequest struct {
	CandidateDiscoveryID string `json:"candidate_discovery_id"`
	ProximityEvidenceID  string `json:"proximity_evidence_id"`
}

func (p pairingCreateRequest) Validate() error {
	p.CandidateDiscoveryID = strings.TrimSpace(p.CandidateDiscoveryID)
	p.ProximityEvidenceID = strings.TrimSpace(p.ProximityEvidenceID)
	if len(p.CandidateDiscoveryID) != 32 {
		return fmt.Errorf("candidate_discovery_id must be a 32-byte session alias")
	}
	for _, r := range p.CandidateDiscoveryID {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return fmt.Errorf("candidate_discovery_id must be hexadecimal")
		}
	}
	if p.ProximityEvidenceID == "" || len(p.ProximityEvidenceID) > 256 {
		return fmt.Errorf("proximity_evidence_id must be 1..256 bytes")
	}
	return nil
}

type pairingConfirmationRequest struct {
	SessionID         string `json:"session_id"`
	ConfirmationNonce string `json:"confirmation_nonce"`
}

func (p pairingConfirmationRequest) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" || len(p.SessionID) > 128 {
		return fmt.Errorf("session_id must be 1..128 bytes")
	}
	if len(strings.TrimSpace(p.ConfirmationNonce)) < 16 || len(p.ConfirmationNonce) > 256 {
		return fmt.Errorf("confirmation_nonce must be 16..256 bytes")
	}
	return nil
}

type pairingRejectRequest struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

func (p pairingRejectRequest) Validate() error {
	if strings.TrimSpace(p.SessionID) == "" || len(p.SessionID) > 128 {
		return fmt.Errorf("session_id must be 1..128 bytes")
	}
	if p.Reason != "user_declined" {
		return fmt.Errorf("device pairing rejection must be user_declined")
	}
	return nil
}

func decodePairingCreate(envelope protocol.Envelope) (pairingCreateRequest, error) {
	return protocol.DecodePayload[pairingCreateRequest](envelope)
}

func decodePairingConfirmation(envelope protocol.Envelope) (pairingConfirmationRequest, error) {
	return protocol.DecodePayload[pairingConfirmationRequest](envelope)
}

func decodePairingReject(envelope protocol.Envelope) (pairingRejectRequest, error) {
	return protocol.DecodePayload[pairingRejectRequest](envelope)
}
