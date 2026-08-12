package controlplane

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type FirmwareManifest struct {
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
	Signature       string    `json:"signature,omitempty"`
}

func (m FirmwareManifest) Validate(now time.Time) error {
	if strings.TrimSpace(m.Version) == "" || strings.TrimSpace(m.Channel) == "" || strings.TrimSpace(m.Board) == "" || strings.TrimSpace(m.URL) == "" {
		return fmt.Errorf("version, channel, board and url are required")
	}
	if m.ProtocolMin < 0 || m.SecurityVersion < 0 {
		return fmt.Errorf("protocol_min and security_version cannot be negative")
	}
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != 32 {
		return fmt.Errorf("sha256 must be 64 hex characters")
	}
	u, err := url.Parse(m.URL)
	if err != nil || (u.IsAbs() && u.Scheme != "https") || (!u.IsAbs() && !strings.HasPrefix(u.Path, "/")) {
		return fmt.Errorf("url must be HTTPS or an absolute server path")
	}
	if !m.ExpiresAt.After(now) {
		return fmt.Errorf("firmware metadata expired")
	}
	if m.MetadataVersion <= 0 {
		return fmt.Errorf("metadata_version required")
	}
	if m.Size <= 0 {
		return fmt.Errorf("firmware size required")
	}
	return nil
}
