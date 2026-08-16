package assetbundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestPackValidateRoundTripDeterministic(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m, files := validFixture(t, []string{"en", "vi"})
	one, err := Pack(m, files, "test-key", priv)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Pack(m, files, "test-key", priv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("identical inputs must produce byte-identical bundles")
	}
	report, err := Validate(one, ValidateOptions{Board: "esp32s3", Width: 320, Height: 240, AssetABI: 1, TrustedKeys: map[string]ed25519.PublicKey{"test-key": pub}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Manifest.BundleID != "companion.default" || report.ExpandedBytes <= 0 || len(report.ArchiveSHA256) != 64 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestNegativeCorpus(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	base, files := validFixture(t, []string{"en"})
	valid, err := Pack(base, files, "test-key", priv)
	if err != nil {
		t.Fatal(err)
	}
	opts := ValidateOptions{Board: "esp32s3", Width: 320, Height: 240, AssetABI: 1, TrustedKeys: map[string]ed25519.PublicKey{"test-key": pub}}

	tests := []struct {
		name   string
		bundle func(t *testing.T) []byte
		opts   ValidateOptions
		want   string
	}{
		{"traversal", func(t *testing.T) []byte { return appendEntry(t, valid, "assets/../escape.png", []byte("x"), zip.Store) }, opts, "unsafe archive path"},
		{"duplicate archive path", func(t *testing.T) []byte { return appendEntry(t, valid, "assets/theme.json", []byte("{}"), zip.Store) }, opts, "duplicate archive path"},
		{"wrong digest", func(t *testing.T) []byte { return tamperEntrySameSize(t, valid, "assets/theme.json") }, opts, "digest mismatch"},
		{"bad signature", func(t *testing.T) []byte {
			return mutateManifest(t, valid, func(m *Manifest) { m.Signature.Value = base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)) })
		}, opts, "signature is invalid"},
		{"outer compression", func(t *testing.T) []byte { return rewriteMethod(t, valid, "assets/theme.json", zip.Deflate) }, opts, "ZIP Store"},
		{"extreme dimensions", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			m.Assets[1].Width = 4096
			m.Assets[1].Height = 4096
			f["assets/avatar.png"] = pngHeader(4096, 4096)
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "dimensions exceed budget"},
		{"missing vi glyph", func(t *testing.T) []byte {
			m, f := validFixture(t, []string{"vi"})
			f["assets/ui.ttf"] = ttfFixture([]rune("ABCabc012"))
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "missing required vi glyph"},
		{"incompatible display", func(t *testing.T) []byte { return valid }, ValidateOptions{Board: "esp32s3", Width: 240, Height: 240, AssetABI: 1, TrustedKeys: opts.TrustedKeys}, "incompatible"},
		{"incompatible ABI", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			m.MinAssetABI = 2
			b, e := Pack(m, f, "test-key", priv)
			if e != nil {
				t.Fatal(e)
			}
			return b
		}, opts, "incompatible"},
		{"unsupported executable type", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			m.Assets[0].Type = "wasm"
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "asset metadata is invalid"},
		{"runtime URL in theme", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			f["assets/theme.json"] = []byte(`{"background":"https://example.invalid/a.png"}`)
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "runtime URL"},
		{"invalid schema", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			m.SchemaVersion = 99
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "unsupported schema_version"},
		{"missing provenance", func(t *testing.T) []byte {
			m, f := validFixture(t, nil)
			m.Assets[0].License.Source = ""
			return signInvalidFixture(t, m, f, "test-key", priv)
		}, opts, "requires source and license provenance"},
		{"extra payload", func(t *testing.T) []byte { return appendEntry(t, valid, "assets/extra.png", pngFixture(t, 1, 1), zip.Store) }, opts, "payloads not declared"},
		{"truncated archive", func(t *testing.T) []byte { return valid[:len(valid)/2] }, opts, "invalid asset bundle archive"},
		{"archive byte budget", func(t *testing.T) []byte { return make([]byte, MaxArchiveBytes+1) }, opts, "archive size"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.bundle(t), tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestPackRejectsUnlistedPayload(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m, files := validFixture(t, nil)
	files["assets/extra.png"] = pngFixture(t, 1, 1)
	if _, err := Pack(m, files, "test-key", priv); err == nil || !strings.Contains(err.Error(), "file count") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func FuzzValidateNeverPanics(f *testing.F) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m, files := validFixtureForFuzz()
	valid, _ := Pack(m, files, "test-key", priv)
	f.Add(valid)
	f.Add([]byte("not-a-zip"))
	opts := ValidateOptions{Board: "esp32s3", Width: 320, Height: 240, AssetABI: 1, TrustedKeys: map[string]ed25519.PublicKey{"test-key": pub}}
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = Validate(raw, opts) })
}

func validFixture(t *testing.T, langs []string) (Manifest, map[string][]byte) {
	t.Helper()
	return fixture(langs, func(w, h int) []byte { return pngFixture(t, w, h) })
}

func validFixtureForFuzz() (Manifest, map[string][]byte) {
	return fixture(nil, func(w, h int) []byte {
		var out bytes.Buffer
		_ = png.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)))
		return out.Bytes()
	})
}

func fixture(langs []string, makePNG func(int, int) []byte) (Manifest, map[string][]byte) {
	fontRunes := append(requiredRunes("en"), requiredRunes("vi")...)
	m := Manifest{SchemaVersion: 1, BundleID: "companion.default", Version: "1.0.0", MinAssetABI: 1,
		Targets: []Target{{Board: "esp32s3", Width: 320, Height: 240}},
		Assets: []Asset{
			{Role: "theme.palette", Type: "theme_json", Path: "assets/theme.json", License: License{Source: "generated:test", License: "CC0-1.0"}},
			{Role: "avatar.neutral", Type: "image_png", Path: "assets/avatar.png", Width: 8, Height: 8, License: License{Source: "generated:test", License: "CC0-1.0"}},
			{Role: "font.ui", Type: "font_ttf", Path: "assets/ui.ttf", Languages: langs, License: License{Source: "generated:test", License: "CC0-1.0"}},
		},
	}
	files := map[string][]byte{
		"assets/theme.json": []byte(`{"background":"#101010","foreground":"#f0f0f0"}`),
		"assets/avatar.png": makePNG(8, 8),
		"assets/ui.ttf":     ttfFixture(fontRunes),
	}
	return m, files
}

func pngFixture(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func signInvalidFixture(t *testing.T, m Manifest, files map[string][]byte, keyID string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	for i := range m.Assets {
		data := files[m.Assets[i].Path]
		s := sha256.Sum256(data)
		m.Assets[i].SHA256 = hex.EncodeToString(s[:])
		m.Assets[i].Size = int64(len(data))
	}
	m.Signature = SignatureMetadata{Algorithm: "ed25519", KeyID: keyID}
	unsigned, err := signingBytes(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Signature.Value = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, unsigned))
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return buildZip(t, mb, files, nil)
}

func mutateManifest(t *testing.T, raw []byte, mutate func(*Manifest)) []byte {
	t.Helper()
	mb, files := unzip(t, raw)
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		t.Fatal(err)
	}
	mutate(&m)
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return buildZip(t, mb, files, nil)
}

func appendEntry(t *testing.T, raw []byte, name string, data []byte, method uint16) []byte {
	t.Helper()
	mb, files := unzip(t, raw)
	extra := []zipEntry{{name: name, data: data, method: method}}
	return buildZip(t, mb, files, extra)
}

func tamperEntrySameSize(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	mb, files := unzip(t, raw)
	b := append([]byte(nil), files[name]...)
	if len(b) == 0 {
		t.Fatal("cannot tamper empty entry")
	}
	b[len(b)/2] ^= 1
	files[name] = b
	return buildZip(t, mb, files, nil)
}

func pngHeader(w, h int) []byte {
	b := make([]byte, 24)
	copy(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10})
	copy(b[12:16], []byte("IHDR"))
	binary.BigEndian.PutUint32(b[16:20], uint32(w))
	binary.BigEndian.PutUint32(b[20:24], uint32(h))
	return b
}

func rewriteMethod(t *testing.T, raw []byte, name string, method uint16) []byte {
	t.Helper()
	mb, files := unzip(t, raw)
	methods := map[string]uint16{name: method}
	return buildZipMethods(t, mb, files, methods, nil)
}

type zipEntry struct {
	name   string
	data   []byte
	method uint16
}

func buildZip(t *testing.T, manifest []byte, files map[string][]byte, extra []zipEntry) []byte {
	return buildZipMethods(t, manifest, files, nil, extra)
}

func buildZipMethods(t *testing.T, manifest []byte, files map[string][]byte, methods map[string]uint16, extra []zipEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	write := func(name string, data []byte, method uint16) {
		h := &zip.FileHeader{Name: name, Method: method}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", manifest, zip.Store)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		method := uint16(zip.Store)
		if methods != nil && methods[n] != 0 {
			method = methods[n]
		}
		write(n, files[n], method)
	}
	for _, e := range extra {
		write(e.name, e.data, e.method)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func unzip(t *testing.T, raw []byte) ([]byte, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	var manifest []byte
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(r); err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
		if f.Name == "manifest.json" {
			manifest = b.Bytes()
		} else {
			files[f.Name] = b.Bytes()
		}
	}
	return manifest, files
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func ttfFixture(runes []rune) []byte {
	uniq := map[rune]bool{}
	for _, r := range runes {
		uniq[r] = true
	}
	ordered := make([]rune, 0, len(uniq))
	for r := range uniq {
		ordered = append(ordered, r)
	}
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j] < ordered[j-1]; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	formatLen := 16 + len(ordered)*12
	cmapLen := 12 + formatLen
	cmap := make([]byte, cmapLen)
	binary.BigEndian.PutUint16(cmap[0:2], 0)
	binary.BigEndian.PutUint16(cmap[2:4], 1)
	binary.BigEndian.PutUint16(cmap[4:6], 3)
	binary.BigEndian.PutUint16(cmap[6:8], 10)
	binary.BigEndian.PutUint32(cmap[8:12], 12)
	f := cmap[12:]
	binary.BigEndian.PutUint16(f[0:2], 12)
	binary.BigEndian.PutUint32(f[4:8], uint32(formatLen))
	binary.BigEndian.PutUint32(f[12:16], uint32(len(ordered)))
	for i, r := range ordered {
		off := 16 + i*12
		binary.BigEndian.PutUint32(f[off:off+4], uint32(r))
		binary.BigEndian.PutUint32(f[off+4:off+8], uint32(r))
		binary.BigEndian.PutUint32(f[off+8:off+12], uint32(i+1))
	}
	out := make([]byte, 28+cmapLen)
	binary.BigEndian.PutUint32(out[0:4], 0x00010000)
	binary.BigEndian.PutUint16(out[4:6], 1)
	copy(out[12:16], []byte("cmap"))
	binary.BigEndian.PutUint32(out[20:24], 28)
	binary.BigEndian.PutUint32(out[24:28], uint32(cmapLen))
	copy(out[28:], cmap)
	return out
}

func ExamplePack() {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m, files := validFixtureForFuzz()
	raw, _ := Pack(m, files, "example-key", priv)
	r, err := Validate(raw, ValidateOptions{Board: "esp32s3", Width: 320, Height: 240, AssetABI: 1, TrustedKeys: map[string]ed25519.PublicKey{"example-key": pub}})
	fmt.Println(err == nil, r.Manifest.BundleID)
	// Output: true companion.default
}
