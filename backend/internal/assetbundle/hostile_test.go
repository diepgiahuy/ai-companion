package assetbundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPackRejectsHostileInputs(t *testing.T) {
	limits := DefaultLimits()
	cases := []struct {
		name  string
		asset SourceAsset
		want  string
	}{
		{"traversal", SourceAsset{Role: "theme.bad", Type: "theme/json", Path: "assets/../evil.json", Data: []byte(`{}`), License: testLicense()}, "unsafe path"},
		{"script type", SourceAsset{Role: "evil", Type: "application/wasm", Path: "assets/evil.wasm", Data: []byte("wasm"), License: testLicense()}, "unsupported asset type"},
		{"missing license", SourceAsset{Role: "theme.bad", Type: "theme/json", Path: "assets/bad.json", Data: []byte(`{}`)}, "license id/source required"},
		{"bad theme", SourceAsset{Role: "theme.bad", Type: "theme/json", Path: "assets/bad.json", Data: []byte(`not-json`), License: testLicense()}, "invalid theme JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Pack(testManifest(), []SourceAsset{tc.asset}, testKey(), limits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %q", err, tc.want)
			}
		})
	}
	badURL := themeAsset()
	badURL.License.Source = "http://example.test/license"
	if _, err := Pack(testManifest(), []SourceAsset{badURL}, testKey(), limits); err == nil || !strings.Contains(err.Error(), "license source") {
		t.Fatalf("license URL: %v", err)
	}
	badTheme := themeAsset()
	badTheme.Data = []byte(`{"palette":{"background":"https://example.test/x"}}`)
	if _, err := Pack(testManifest(), []SourceAsset{badTheme}, testKey(), limits); err == nil || !strings.Contains(err.Error(), "palette") {
		t.Fatalf("theme arbitrary URL/value: %v", err)
	}
	huge := themeAsset()
	huge.Data = bytes.Repeat([]byte("x"), int(limits.MaxAssetBytes)+1)
	if _, err := Pack(testManifest(), []SourceAsset{huge}, testKey(), limits); err == nil || !strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := Pack(testManifest(), []SourceAsset{pngAsset("background.huge", "assets/huge.png", limits.MaxImageWidth+1, 1)}, testKey(), limits); err == nil || !strings.Contains(err.Error(), "image dimensions invalid") {
		t.Fatalf("dimensions: %v", err)
	}
	missing := SourceAsset{Role: "font.primary", Type: "font/ttf", Path: "assets/font.ttf", Data: fontFixture(t, "NotoSans-EN-subset.ttf"), GlyphProfiles: []string{"vi"}, License: License{ID: "OFL-1.1", Source: "https://github.com/notofonts/latin-greek-cyrillic"}}
	if _, err := Pack(testManifest(), []SourceAsset{missing}, testKey(), limits); err == nil || !strings.Contains(err.Error(), "missing glyph") {
		t.Fatalf("missing glyph: %v", err)
	}
}

func TestValidateRejectsCorruptionAndCompatibilityMismatch(t *testing.T) {
	raw, err := Pack(testManifest(), []SourceAsset{themeAsset(), pngAsset("avatar.neutral", "assets/neutral.png", 8, 8)}, testKey(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	pub := testKey().Public().(ed25519.PublicKey)
	bad := append([]byte(nil), raw...)
	bad[len(bad)/2] ^= 0xff
	if _, err := Validate(bad, pub, validationTarget(), DefaultLimits()); err == nil {
		t.Fatal("corrupted archive must fail")
	}
	if _, err := Validate(raw, pub, ValidationTarget{Board: "esp32-s3", DisplayWidth: 320, DisplayHeight: 240, AssetABI: 1}, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "incompatible display") {
		t.Fatalf("display mismatch: %v", err)
	}
	if _, err := Validate(raw, pub, ValidationTarget{Board: "esp32-s3", DisplayWidth: 240, DisplayHeight: 240, AssetABI: 0}, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "incompatible asset ABI") {
		t.Fatalf("ABI mismatch: %v", err)
	}
	if _, err := Validate(raw[:len(raw)/2], pub, validationTarget(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "corrupt zip") {
		t.Fatalf("truncation: %v", err)
	}
	otherSeed := testSeed
	otherSeed[0] = 99
	other := ed25519.NewKeyFromSeed(otherSeed[:]).Public().(ed25519.PublicKey)
	if _, err := Validate(raw, other, validationTarget(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "signature invalid") {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestValidateRejectsTamperedManifestArchiveShapesAndAccounting(t *testing.T) {
	raw, err := Pack(testManifest(), []SourceAsset{themeAsset()}, testKey(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	pub := testKey().Public().(ed25519.PublicKey)
	t.Run("wrong digest", func(t *testing.T) {
		mut := rewriteZip(t, raw, func(name string, data []byte, method uint16) ([]byte, uint16) {
			if name == "assets/theme.json" {
				return []byte(`{"changed":true}`), method
			}
			return data, method
		})
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "size mismatch") && !strings.Contains(err.Error(), "sha256 mismatch") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("total bundle bytes", func(t *testing.T) {
		limits := DefaultLimits()
		huge := append([]byte(nil), raw...)
		huge = append(huge, make([]byte, int(limits.MaxBundleBytes)-len(huge)+1)...)
		_, err := Validate(huge, pub, validationTarget(), limits)
		if err == nil || !strings.Contains(err.Error(), "bundle exceeds total byte limit") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("symlink entry", func(t *testing.T) {
		mut := rewriteZipWithHeader(t, raw, func(h *zip.FileHeader) {
			if h.Name == "assets/theme.json" {
				h.SetMode(os.ModeSymlink | 0777)
			}
		})
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "non-regular archive entry") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("compressed entry", func(t *testing.T) {
		mut := rewriteZip(t, raw, func(name string, data []byte, method uint16) ([]byte, uint16) {
			if name == "assets/theme.json" {
				return data, zip.Deflate
			}
			return data, method
		})
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "compressed zip entries are not allowed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad signature metadata", func(t *testing.T) {
		mut := rewriteSignedManifest(t, raw, func(m *Manifest) { m.Signature.Algorithm = "ecdsa" })
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "unsupported signature metadata") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad schema", func(t *testing.T) {
		mut := rewriteSignedManifest(t, raw, func(m *Manifest) { m.SchemaVersion = 99 })
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("expanded accounting", func(t *testing.T) {
		mut := rewriteSignedManifest(t, raw, func(m *Manifest) { m.ExpandedBytes++ })
		_, err := Validate(mut, pub, validationTarget(), DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "expanded byte accounting mismatch") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestPackRejectsDuplicatePathAndRole(t *testing.T) {
	a := themeAsset()
	b := themeAsset()
	b.Role = "theme.other"
	if _, err := Pack(testManifest(), []SourceAsset{a, b}, testKey(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "duplicate path") {
		t.Fatalf("duplicate path: %v", err)
	}
	b.Path = "assets/theme2.json"
	b.Role = a.Role
	if _, err := Pack(testManifest(), []SourceAsset{a, b}, testKey(), DefaultLimits()); err == nil || !strings.Contains(err.Error(), "duplicate asset role") {
		t.Fatalf("duplicate role: %v", err)
	}
}

func rewriteSignedManifest(t *testing.T, raw []byte, mutate func(*Manifest)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	entries := map[string][]byte{}
	order := []string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := ioReadAll(r)
		r.Close()
		entries[f.Name] = b
		order = append(order, f.Name)
	}
	if err := json.Unmarshal(entries[ManifestName], &m); err != nil {
		t.Fatal(err)
	}
	mutate(&m)
	canonical, err := canonicalManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	entries[ManifestName] = canonical
	entries[SignatureName] = ed25519.Sign(testKey(), canonical)
	return zipEntries(t, order, entries, nil)
}
func rewriteZip(t *testing.T, raw []byte, mutate func(string, []byte, uint16) ([]byte, uint16)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	methods := map[string]uint16{}
	order := []string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := ioReadAll(r)
		r.Close()
		b, m := mutate(f.Name, b, f.Method)
		entries[f.Name] = b
		methods[f.Name] = m
		order = append(order, f.Name)
	}
	return zipEntries(t, order, entries, methods)
}
func rewriteZipWithHeader(t *testing.T, raw []byte, mutate func(*zip.FileHeader)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		h := f.FileHeader
		mutate(&h)
		w, err := zw.CreateHeader(&h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func zipEntries(t *testing.T, order []string, entries map[string][]byte, methods map[string]uint16) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, name := range order {
		method := uint16(zip.Store)
		if methods != nil {
			method = methods[name]
		}
		h := &zip.FileHeader{Name: name, Method: method}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func ioReadAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(r)
	return b.Bytes(), err
}

func FuzzValidateNeverPanics(f *testing.F) {
	raw, err := Pack(testManifest(), []SourceAsset{themeAsset()}, testKey(), DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte("not-a-zip"))
	pub := testKey().Public().(ed25519.PublicKey)
	target := validationTarget()
	limits := DefaultLimits()
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = Validate(b, pub, target, limits) })
}
