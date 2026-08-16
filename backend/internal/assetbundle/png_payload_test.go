package assetbundle

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestValidateAssetContentRejectsTruncatedPNGPixelStream(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	valid := encoded.Bytes()
	if len(valid) < 2 {
		t.Fatal("encoded PNG unexpectedly short")
	}
	truncated := valid[:len(valid)-1]

	// This is the exact regression: metadata parsing still succeeds even though
	// full pixel decoding rejects the truncated payload.
	cfg, err := png.DecodeConfig(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("fixture must pass DecodeConfig: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(truncated)); err == nil {
		t.Fatal("fixture must fail full PNG decode")
	}

	a := Asset{
		Role:   "avatar.neutral",
		Type:   "image/png",
		Path:   "assets/avatar.png",
		SHA256: strings.Repeat("0", 64),
		Size:   int64(len(truncated)),
		Width:  cfg.Width,
		Height: cfg.Height,
		License: License{
			ID:     "CC0-1.0",
			Source: "generated:test",
		},
	}
	if err := validateAssetContent(a, truncated, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "corrupt PNG payload") {
		t.Fatalf("expected corrupt PNG payload rejection, got %v", err)
	}
}
