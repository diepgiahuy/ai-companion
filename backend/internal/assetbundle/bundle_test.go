package assetbundle

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"testing"
)

var testSeed = [ed25519.SeedSize]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}

func testKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(testSeed[:]) }
func testManifest() Manifest {
	return Manifest{BundleID: "desk-face", Version: "1.0.0", MinAssetABI: 1, Target: Target{Board: "esp32-s3", DisplayWidth: 240, DisplayHeight: 240}, Signature: Signature{KeyID: "test-key"}}
}
func testLicense() License { return License{ID: "CC0-1.0", Source: "generated:test"} }
func themeAsset() SourceAsset {
	return SourceAsset{Role: "theme.default", Type: "theme/json", Path: "assets/theme.json", Data: []byte(`{"palette":{"background":"#101010","foreground":"#f0f0f0"}}`), License: testLicense()}
}
func pngAsset(role, p string, w, h int) SourceAsset {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 255), G: uint8(y % 255), B: 0x77, A: 0xff})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return SourceAsset{Role: role, Type: "image/png", Path: p, Data: b.Bytes(), License: testLicense()}
}
func fontFixture(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func validationTarget() ValidationTarget {
	return ValidationTarget{Board: "esp32-s3", DisplayWidth: 240, DisplayHeight: 240, AssetABI: 1}
}

func TestPackValidateInspectRoundTripIsDeterministic(t *testing.T) {
	font := SourceAsset{Role: "font.primary", Type: "font/ttf", Path: "assets/font.ttf", Data: fontFixture(t, "NotoSans-VI-EN-subset.ttf"), GlyphProfiles: []string{"vi", "en"}, License: License{ID: "OFL-1.1", Source: "https://github.com/notofonts/latin-greek-cyrillic"}}
	assets := []SourceAsset{pngAsset("avatar.happy", "assets/happy.png", 16, 16), themeAsset(), font}
	a, err := Pack(testManifest(), assets, testKey(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Pack(testManifest(), assets, testKey(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("identical inputs must produce identical signed bundle bytes")
	}
	got, err := Validate(a, testKey().Public().(ed25519.PublicKey), validationTarget(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.BundleID != "desk-face" || len(got.Manifest.Assets) != 3 || got.BundleSHA256 == "" || got.ManifestSHA256 == "" {
		t.Fatalf("unexpected validation result: %+v", got)
	}
	inspected, err := Inspect(a, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if inspected.BundleID != got.Manifest.BundleID || len(inspected.Assets) != 3 {
		t.Fatalf("inspect mismatch: %+v", inspected)
	}
}
