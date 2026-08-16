package assetbundle

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
)

func TestPackRejectsInvalidPayloadAndDuplicateRole(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	m, files := validFixture(t, nil)
	files["assets/avatar.png"] = pngHeader(8, 8)
	if _, err := Pack(m, files, "test-key", priv); err == nil || !strings.Contains(err.Error(), "PNG") {
		t.Fatalf("expected corrupt PNG rejection before signing, got %v", err)
	}

	m, files = validFixture(t, nil)
	m.Assets[1].Role = m.Assets[0].Role
	if _, err := Pack(m, files, "test-key", priv); err == nil || !strings.Contains(err.Error(), "duplicate asset role") {
		t.Fatalf("expected duplicate role rejection before signing, got %v", err)
	}

	m, files = validFixture(t, nil)
	files["assets/theme.json"] = make([]byte, MaxAssetBytes+1)
	if _, err := Pack(m, files, "test-key", priv); err == nil || !strings.Contains(err.Error(), "size is outside allowed budget") {
		t.Fatalf("expected per-entry budget rejection before signing, got %v", err)
	}
}

func TestValidateRejectsSignedTruncatedPNG(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m, files := validFixture(t, nil)
	files["assets/avatar.png"] = pngHeader(8, 8)
	raw := signInvalidFixture(t, m, files, "test-key", priv)

	_, err = Validate(raw, ValidateOptions{
		Board:    "esp32s3",
		Width:    320,
		Height:   240,
		AssetABI: 1,
		TrustedKeys: map[string]ed25519.PublicKey{
			"test-key": pub,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "PNG") {
		t.Fatalf("expected signed truncated PNG rejection, got %v", err)
	}
}
