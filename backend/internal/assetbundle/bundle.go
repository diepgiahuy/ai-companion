package assetbundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	SchemaVersion      = 1
	ManifestName       = "manifest.json"
	SignatureName      = "manifest.sig"
	SignatureAlgorithm = "ed25519"
)

var ErrInvalidBundle = errors.New("invalid asset bundle")

type Limits struct {
	MaxBundleBytes   int64
	MaxManifestBytes int64
	MaxFiles         int
	MaxPathBytes     int
	MaxAssetBytes    int64
	MaxExpandedBytes int64
	MaxImageWidth    int
	MaxImageHeight   int
}

func DefaultLimits() Limits {
	return Limits{MaxBundleBytes: 3 << 20, MaxManifestBytes: 64 << 10, MaxFiles: 64, MaxPathBytes: 128, MaxAssetBytes: 1 << 20, MaxExpandedBytes: 2 << 20, MaxImageWidth: 1024, MaxImageHeight: 1024}
}

type Target struct {
	Board         string `json:"board"`
	DisplayWidth  int    `json:"display_width,omitempty"`
	DisplayHeight int    `json:"display_height,omitempty"`
}

type License struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Attribution string `json:"attribution,omitempty"`
}

type Asset struct {
	Role          string   `json:"role"`
	Type          string   `json:"type"`
	Path          string   `json:"path"`
	SHA256        string   `json:"sha256"`
	Size          int64    `json:"size"`
	Width         int      `json:"width,omitempty"`
	Height        int      `json:"height,omitempty"`
	GlyphProfiles []string `json:"glyph_profiles,omitempty"`
	License       License  `json:"license"`
	FallbackRole  string   `json:"fallback_role,omitempty"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
}

type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	BundleID      string    `json:"bundle_id"`
	Version       string    `json:"version"`
	MinAssetABI   int       `json:"min_asset_abi"`
	Target        Target    `json:"target"`
	ExpandedBytes int64     `json:"expanded_bytes"`
	Assets        []Asset   `json:"assets"`
	Signature     Signature `json:"signature"`
}

type ValidationTarget struct {
	Board         string
	DisplayWidth  int
	DisplayHeight int
	AssetABI      int
}

type Validated struct {
	Manifest       Manifest
	ManifestSHA256 string
	BundleSHA256   string
}

type SourceAsset struct {
	Role          string
	Type          string
	Path          string
	Data          []byte
	GlyphProfiles []string
	License       License
	FallbackRole  string
}

func Pack(m Manifest, assets []SourceAsset, privateKey ed25519.PrivateKey, limits Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidBundle)
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("%w: assets required", ErrInvalidBundle)
	}
	m.SchemaVersion = SchemaVersion
	m.Signature.Algorithm = SignatureAlgorithm
	m.Assets = make([]Asset, 0, len(assets))
	var expanded int64
	copied := append([]SourceAsset(nil), assets...)
	sort.Slice(copied, func(i, j int) bool { return copied[i].Path < copied[j].Path })
	for _, src := range copied {
		if int64(len(src.Data)) > limits.MaxAssetBytes {
			return nil, fmt.Errorf("%w: asset %q exceeds per-file limit", ErrInvalidBundle, src.Path)
		}
		sum := sha256.Sum256(src.Data)
		a := Asset{Role: src.Role, Type: src.Type, Path: src.Path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(src.Data)), GlyphProfiles: append([]string(nil), src.GlyphProfiles...), License: src.License, FallbackRole: src.FallbackRole}
		if src.Type == "image/png" {
			cfg, err := png.DecodeConfig(bytes.NewReader(src.Data))
			if err != nil {
				return nil, fmt.Errorf("%w: asset %q invalid PNG: %v", ErrInvalidBundle, src.Path, err)
			}
			a.Width, a.Height = cfg.Width, cfg.Height
		}
		m.Assets = append(m.Assets, a)
		expanded += int64(len(src.Data))
	}
	m.ExpandedBytes = expanded
	canonical, err := canonicalManifest(m)
	if err != nil {
		return nil, err
	}
	if int64(len(canonical)) > limits.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest exceeds limit", ErrInvalidBundle)
	}
	sig := ed25519.Sign(privateKey, canonical)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	entries := []struct {
		name string
		data []byte
	}{{ManifestName, canonical}, {SignatureName, sig}}
	for _, src := range copied {
		entries = append(entries, struct {
			name string
			data []byte
		}{src.Path, src.Data})
	}
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err = w.Write(e.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if int64(out.Len()) > limits.MaxBundleBytes {
		return nil, fmt.Errorf("%w: bundle exceeds total byte limit", ErrInvalidBundle)
	}
	if _, err := Validate(out.Bytes(), ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey), ValidationTarget{Board: m.Target.Board, DisplayWidth: m.Target.DisplayWidth, DisplayHeight: m.Target.DisplayHeight, AssetABI: m.MinAssetABI}, limits); err != nil {
		return nil, fmt.Errorf("pack produced invalid bundle: %w", err)
	}
	return out.Bytes(), nil
}

func Validate(raw []byte, publicKey ed25519.PublicKey, target ValidationTarget, limits Limits) (Validated, error) {
	if err := validateLimits(limits); err != nil {
		return Validated{}, err
	}
	if int64(len(raw)) > limits.MaxBundleBytes {
		return Validated{}, fmt.Errorf("%w: bundle exceeds total byte limit", ErrInvalidBundle)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Validated{}, fmt.Errorf("%w: trusted Ed25519 public key required", ErrInvalidBundle)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Validated{}, fmt.Errorf("%w: corrupt zip: %v", ErrInvalidBundle, err)
	}
	if len(zr.File) < 3 || len(zr.File) > limits.MaxFiles+2 {
		return Validated{}, fmt.Errorf("%w: file count out of bounds", ErrInvalidBundle)
	}
	files := make(map[string]*zip.File, len(zr.File))
	var total int64
	for _, f := range zr.File {
		if mode := f.Mode(); mode.IsDir() || mode&os.ModeType != 0 {
			return Validated{}, fmt.Errorf("%w: non-regular archive entry %q", ErrInvalidBundle, f.Name)
		}
		if f.Method != zip.Store {
			return Validated{}, fmt.Errorf("%w: compressed zip entries are not allowed: %q", ErrInvalidBundle, f.Name)
		}
		if err := validatePath(f.Name, limits.MaxPathBytes); err != nil {
			return Validated{}, err
		}
		if _, ok := files[f.Name]; ok {
			return Validated{}, fmt.Errorf("%w: duplicate path %q", ErrInvalidBundle, f.Name)
		}
		if f.UncompressedSize64 != f.CompressedSize64 {
			return Validated{}, fmt.Errorf("%w: compressed entry %q rejected", ErrInvalidBundle, f.Name)
		}
		if f.Name != ManifestName && f.Name != SignatureName {
			if f.UncompressedSize64 > uint64(limits.MaxAssetBytes) {
				return Validated{}, fmt.Errorf("%w: asset %q exceeds per-file limit", ErrInvalidBundle, f.Name)
			}
			total += int64(f.UncompressedSize64)
			if total > limits.MaxExpandedBytes {
				return Validated{}, fmt.Errorf("%w: expanded bytes exceed limit", ErrInvalidBundle)
			}
		}
		files[f.Name] = f
	}
	manifestRaw, err := readBounded(files[ManifestName], limits.MaxManifestBytes)
	if err != nil {
		return Validated{}, err
	}
	sig, err := readBounded(files[SignatureName], ed25519.SignatureSize)
	if err != nil {
		return Validated{}, err
	}
	if len(sig) != ed25519.SignatureSize {
		return Validated{}, fmt.Errorf("%w: signature must be %d bytes", ErrInvalidBundle, ed25519.SignatureSize)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestRaw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Validated{}, fmt.Errorf("%w: manifest JSON: %v", ErrInvalidBundle, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return Validated{}, fmt.Errorf("%w: trailing manifest JSON", ErrInvalidBundle)
	}
	canonical, err := canonicalManifest(m)
	if err != nil {
		return Validated{}, err
	}
	if !bytes.Equal(canonical, manifestRaw) {
		return Validated{}, fmt.Errorf("%w: manifest is not canonical JSON", ErrInvalidBundle)
	}
	if !ed25519.Verify(publicKey, manifestRaw, sig) {
		return Validated{}, fmt.Errorf("%w: manifest signature invalid", ErrInvalidBundle)
	}
	if err := validateManifest(m, target, limits); err != nil {
		return Validated{}, err
	}
	declared := make(map[string]Asset, len(m.Assets))
	roles := make(map[string]struct{}, len(m.Assets))
	var declaredTotal int64
	for _, a := range m.Assets {
		if _, exists := declared[a.Path]; exists {
			return Validated{}, fmt.Errorf("%w: duplicate manifest path %q", ErrInvalidBundle, a.Path)
		}
		if _, exists := roles[a.Role]; exists {
			return Validated{}, fmt.Errorf("%w: duplicate asset role %q", ErrInvalidBundle, a.Role)
		}
		roles[a.Role] = struct{}{}
		declared[a.Path] = a
		f := files[a.Path]
		if f == nil {
			return Validated{}, fmt.Errorf("%w: missing asset %q", ErrInvalidBundle, a.Path)
		}
		data, err := readBounded(f, limits.MaxAssetBytes)
		if err != nil {
			return Validated{}, err
		}
		declaredTotal += int64(len(data))
		if int64(len(data)) != a.Size {
			return Validated{}, fmt.Errorf("%w: size mismatch for %q", ErrInvalidBundle, a.Path)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != strings.ToLower(a.SHA256) {
			return Validated{}, fmt.Errorf("%w: sha256 mismatch for %q", ErrInvalidBundle, a.Path)
		}
		if err := validateAssetContent(a, data, limits); err != nil {
			return Validated{}, err
		}
	}
	if declaredTotal != total || declaredTotal != m.ExpandedBytes {
		return Validated{}, fmt.Errorf("%w: expanded byte accounting mismatch", ErrInvalidBundle)
	}
	for _, a := range m.Assets {
		if a.FallbackRole == "" {
			continue
		}
		if a.FallbackRole == a.Role {
			return Validated{}, fmt.Errorf("%w: asset %q cannot fall back to itself", ErrInvalidBundle, a.Role)
		}
		if _, ok := roles[a.FallbackRole]; !ok && !strings.HasPrefix(a.FallbackRole, "factory.") {
			return Validated{}, fmt.Errorf("%w: unknown fallback role %q", ErrInvalidBundle, a.FallbackRole)
		}
	}
	for name := range files {
		if name != ManifestName && name != SignatureName {
			if _, ok := declared[name]; !ok {
				return Validated{}, fmt.Errorf("%w: undeclared archive entry %q", ErrInvalidBundle, name)
			}
		}
	}
	ms := sha256.Sum256(manifestRaw)
	bs := sha256.Sum256(raw)
	return Validated{Manifest: m, ManifestSHA256: hex.EncodeToString(ms[:]), BundleSHA256: hex.EncodeToString(bs[:])}, nil
}

func Inspect(raw []byte, limits Limits) (Manifest, error) {
	if err := validateLimits(limits); err != nil {
		return Manifest{}, err
	}
	if int64(len(raw)) > limits.MaxBundleBytes {
		return Manifest{}, fmt.Errorf("%w: bundle exceeds total byte limit", ErrInvalidBundle)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: corrupt zip: %v", ErrInvalidBundle, err)
	}
	var mf *zip.File
	for _, f := range zr.File {
		if f.Name == ManifestName {
			if mf != nil {
				return Manifest{}, fmt.Errorf("%w: duplicate manifest", ErrInvalidBundle)
			}
			mf = f
		}
	}
	b, err := readBounded(mf, limits.MaxManifestBytes)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest JSON: %v", ErrInvalidBundle, err)
	}
	return m, nil
}

func canonicalManifest(m Manifest) ([]byte, error) {
	sort.Slice(m.Assets, func(i, j int) bool { return m.Assets[i].Path < m.Assets[j].Path })
	for i := range m.Assets {
		sort.Strings(m.Assets[i].GlyphProfiles)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal manifest: %v", ErrInvalidBundle, err)
	}
	return b, nil
}

func validateLimits(l Limits) error {
	if l.MaxBundleBytes <= 0 || l.MaxManifestBytes <= 0 || l.MaxFiles <= 0 || l.MaxPathBytes <= 0 || l.MaxAssetBytes <= 0 || l.MaxExpandedBytes <= 0 || l.MaxImageWidth <= 0 || l.MaxImageHeight <= 0 {
		return fmt.Errorf("%w: invalid validator limits", ErrInvalidBundle)
	}
	if l.MaxBundleBytes <= l.MaxExpandedBytes || l.MaxAssetBytes > l.MaxExpandedBytes {
		return fmt.Errorf("%w: inconsistent validator limits", ErrInvalidBundle)
	}
	return nil
}

func validateManifest(m Manifest, target ValidationTarget, limits Limits) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidBundle, m.SchemaVersion)
	}
	if !safeID(m.BundleID, 64) || !safeVersion(m.Version) {
		return fmt.Errorf("%w: invalid bundle id/version", ErrInvalidBundle)
	}
	if m.MinAssetABI <= 0 || target.AssetABI < m.MinAssetABI {
		return fmt.Errorf("%w: incompatible asset ABI", ErrInvalidBundle)
	}
	if !safeID(m.Target.Board, 64) || target.Board != m.Target.Board {
		return fmt.Errorf("%w: incompatible board", ErrInvalidBundle)
	}
	if m.Target.DisplayWidth < 0 || m.Target.DisplayHeight < 0 || (m.Target.DisplayWidth == 0) != (m.Target.DisplayHeight == 0) {
		return fmt.Errorf("%w: invalid display target", ErrInvalidBundle)
	}
	if m.Target.DisplayWidth > 0 && (target.DisplayWidth != m.Target.DisplayWidth || target.DisplayHeight != m.Target.DisplayHeight) {
		return fmt.Errorf("%w: incompatible display", ErrInvalidBundle)
	}
	if len(m.Assets) == 0 || len(m.Assets) > limits.MaxFiles {
		return fmt.Errorf("%w: asset count out of bounds", ErrInvalidBundle)
	}
	if m.ExpandedBytes <= 0 || m.ExpandedBytes > limits.MaxExpandedBytes {
		return fmt.Errorf("%w: expanded_bytes out of bounds", ErrInvalidBundle)
	}
	if m.Signature.Algorithm != SignatureAlgorithm || !safeID(m.Signature.KeyID, 64) {
		return fmt.Errorf("%w: unsupported signature metadata", ErrInvalidBundle)
	}
	return nil
}
