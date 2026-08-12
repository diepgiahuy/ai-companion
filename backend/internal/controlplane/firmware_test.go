package controlplane

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestFirmwareManifestSignatureAndExpiry(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := FirmwareManifest{Version: "1.2.3", Channel: "stable", Board: "esp32-s3", ProtocolMin: 1, SecurityVersion: 2, URL: "https://example/fw.bin", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 123, ExpiresAt: time.Now().Add(time.Hour), MetadataVersion: 7}
	if e := m.Sign(priv); e != nil || !m.Verify(pub) {
		t.Fatalf("sign=%v verify=%v", e, m.Verify(pub))
	}
	m.Size++
	if m.Verify(pub) {
		t.Fatal("tamper must fail")
	}
}
