package assetbundle

import (
	"compress/gzip"
	"crypto/ed25519"
	"io"
	"os"
	"strings"
	"testing"
)

func TestFallbackRoleSemantics(t *testing.T) {
	base := themeAsset()
	base.FallbackRole = "factory.theme.default"
	if _, err := Pack(testManifest(), []SourceAsset{base}, testKey(), DefaultLimits()); err != nil {
		t.Fatalf("factory fallback should be accepted: %v", err)
	}

	self := themeAsset()
	self.FallbackRole = self.Role
	if _, err := Pack(testManifest(), []SourceAsset{self}, testKey(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "cannot fall back to itself") {
		t.Fatalf("self fallback: %v", err)
	}

	unknown := themeAsset()
	unknown.FallbackRole = "theme.missing"
	if _, err := Pack(testManifest(), []SourceAsset{unknown}, testKey(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "unknown fallback role") {
		t.Fatalf("unknown fallback: %v", err)
	}

	secondary := themeAsset()
	secondary.Role = "theme.safe"
	secondary.Path = "assets/safe.json"
	base = themeAsset()
	base.FallbackRole = secondary.Role
	if _, err := Pack(testManifest(), []SourceAsset{base, secondary}, testKey(), DefaultLimits()); err != nil {
		t.Fatalf("declared-role fallback should be accepted: %v", err)
	}
}

func FuzzFontCoversNeverPanics(f *testing.F) {
	fixture, err := os.Open("testdata/NotoSans-VI-EN-subset.ttf.gz")
	if err != nil {
		f.Fatal(err)
	}
	zr, err := gzip.NewReader(fixture)
	if err != nil {
		fixture.Close()
		f.Fatal(err)
	}
	font, err := io.ReadAll(zr)
	zr.Close()
	fixture.Close()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(font)
	f.Add([]byte("not-a-font"))
	f.Add([]byte{0, 1, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		_ = fontCovers(b, []rune{'A', 'Đ', 'ự'})
	})
}

func TestValidateUsesExactRawSignatureBytes(t *testing.T) {
	raw, err := Pack(testManifest(), []SourceAsset{themeAsset()}, testKey(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(raw, testKey().Public().(ed25519.PublicKey), validationTarget(), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}
