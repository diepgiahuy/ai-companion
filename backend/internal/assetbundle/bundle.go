package assetbundle

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion     = 1
	MaxArchiveBytes   = 2 << 20
	MaxManifestBytes  = 64 << 10
	MaxAssetBytes     = 1 << 20
	MaxExpandedBytes  = 2 << 20
	MaxEntries        = 64
	MaxPathBytes      = 160
	MaxImageDimension = 2048
	MaxImagePixels    = 1_048_576
)

var allowedTypes = map[string]bool{
	"theme_json": true,
	"image_png":  true,
	"font_ttf":   true,
}

type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	BundleID      string            `json:"bundle_id"`
	Version       string            `json:"version"`
	MinAssetABI   int               `json:"min_asset_abi"`
	Targets       []Target          `json:"targets"`
	Assets        []Asset           `json:"assets"`
	Signature     SignatureMetadata `json:"signature"`
}

type Target struct {
	Board  string `json:"board"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Asset struct {
	Role      string   `json:"role"`
	Type      string   `json:"type"`
	Path      string   `json:"path"`
	SHA256    string   `json:"sha256"`
	Size      int64    `json:"size"`
	Width     int      `json:"width,omitempty"`
	Height    int      `json:"height,omitempty"`
	Languages []string `json:"languages,omitempty"`
	License   License  `json:"license"`
}

type License struct {
	Source      string `json:"source"`
	License     string `json:"license"`
	Attribution string `json:"attribution,omitempty"`
}

type SignatureMetadata struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Value     string `json:"value"`
}

type ValidateOptions struct {
	Board       string
	Width       int
	Height      int
	AssetABI    int
	TrustedKeys map[string]ed25519.PublicKey
}

type Report struct {
	Manifest      Manifest
	ArchiveSHA256 string
	ExpandedBytes int64
}

func Pack(m Manifest, files map[string][]byte, keyID string, privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("asset bundle private key must be %d bytes", ed25519.PrivateKeySize)
	}
	if !safeToken(keyID, 64) {
		return nil, errors.New("asset bundle key_id is invalid")
	}
	if len(files) != len(m.Assets) {
		return nil, fmt.Errorf("asset file count %d does not match manifest count %d", len(files), len(m.Assets))
	}
	seen := make(map[string]bool, len(m.Assets))
	for i := range m.Assets {
		a := &m.Assets[i]
		if seen[a.Path] {
			return nil, fmt.Errorf("duplicate asset path %q", a.Path)
		}
		seen[a.Path] = true
		data, ok := files[a.Path]
		if !ok {
			return nil, fmt.Errorf("missing asset payload %q", a.Path)
		}
		sum := sha256.Sum256(data)
		a.SHA256 = hex.EncodeToString(sum[:])
		a.Size = int64(len(data))
	}
	for name := range files {
		if !seen[name] {
			return nil, fmt.Errorf("unlisted asset payload %q", name)
		}
	}
	m.Signature = SignatureMetadata{Algorithm: "ed25519", KeyID: keyID}
	if err := validateManifestShape(m, false); err != nil {
		return nil, err
	}
	unsigned, err := signingBytes(m)
	if err != nil {
		return nil, err
	}
	m.Signature.Value = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, unsigned))
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(manifestBytes) > MaxManifestBytes {
		return nil, errors.New("manifest exceeds byte budget")
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	if err := writeStored(zw, "manifest.json", manifestBytes); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeStored(zw, name, files[name]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > MaxArchiveBytes {
		return nil, errors.New("archive exceeds byte budget")
	}
	return out.Bytes(), nil
}

func writeStored(zw *zip.Writer, name string, data []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
	h.SetMode(0o644)
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func Validate(raw []byte, opts ValidateOptions) (Report, error) {
	var report Report
	if len(raw) == 0 || len(raw) > MaxArchiveBytes {
		return report, errors.New("archive size is outside allowed budget")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return report, fmt.Errorf("invalid asset bundle archive: %w", err)
	}
	if len(zr.File) == 0 || len(zr.File) > MaxEntries+1 {
		return report, errors.New("archive entry count is outside allowed budget")
	}
	entries := make(map[string][]byte, len(zr.File))
	var expanded int64
	for _, f := range zr.File {
		if !safeArchivePath(f.Name) {
			return report, fmt.Errorf("unsafe archive path %q", f.Name)
		}
		if _, exists := entries[f.Name]; exists {
			return report, fmt.Errorf("duplicate archive path %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			return report, fmt.Errorf("directories are not allowed: %q", f.Name)
		}
		if f.Method != zip.Store || f.CompressedSize64 != f.UncompressedSize64 {
			return report, fmt.Errorf("entry %q must use ZIP Store without outer compression", f.Name)
		}
		limit := uint64(MaxAssetBytes)
		if f.Name == "manifest.json" {
			limit = MaxManifestBytes
		}
		if f.UncompressedSize64 > limit {
			return report, fmt.Errorf("entry %q exceeds byte budget", f.Name)
		}
		expanded += int64(f.UncompressedSize64)
		if expanded > MaxExpandedBytes+MaxManifestBytes {
			return report, errors.New("archive expanded bytes exceed budget")
		}
		rc, err := f.Open()
		if err != nil {
			return report, fmt.Errorf("open %q: %w", f.Name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
		closeErr := rc.Close()
		if readErr != nil {
			return report, fmt.Errorf("read %q: %w", f.Name, readErr)
		}
		if closeErr != nil {
			return report, fmt.Errorf("close %q: %w", f.Name, closeErr)
		}
		if uint64(len(data)) != f.UncompressedSize64 || uint64(len(data)) > limit {
			return report, fmt.Errorf("entry %q length mismatch", f.Name)
		}
		entries[f.Name] = data
	}
	manifestRaw, ok := entries["manifest.json"]
	if !ok {
		return report, errors.New("manifest.json is required")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifestRaw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return report, fmt.Errorf("invalid manifest: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return report, errors.New("manifest must contain exactly one JSON object")
	}
	if err := validateManifestShape(m, true); err != nil {
		return report, err
	}
	pub, ok := opts.TrustedKeys[m.Signature.KeyID]
	if !ok || len(pub) != ed25519.PublicKeySize {
		return report, fmt.Errorf("untrusted asset signing key %q", m.Signature.KeyID)
	}
	sig, err := base64.RawURLEncoding.DecodeString(m.Signature.Value)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return report, errors.New("asset bundle signature encoding is invalid")
	}
	unsigned, err := signingBytes(m)
	if err != nil {
		return report, err
	}
	if !ed25519.Verify(pub, unsigned, sig) {
		return report, errors.New("asset bundle signature is invalid")
	}
	if opts.AssetABI < m.MinAssetABI || !compatible(m.Targets, opts) {
		return report, errors.New("asset bundle is incompatible with target display or ABI")
	}
	if len(entries) != len(m.Assets)+1 {
		return report, errors.New("archive contains payloads not declared by manifest")
	}
	seenPath := map[string]bool{}
	seenRole := map[string]bool{}
	var payloadBytes int64
	for _, a := range m.Assets {
		if seenPath[a.Path] {
			return report, fmt.Errorf("duplicate manifest path %q", a.Path)
		}
		if seenRole[a.Role] {
			return report, fmt.Errorf("duplicate asset role %q", a.Role)
		}
		seenPath[a.Path] = true
		seenRole[a.Role] = true
		data, ok := entries[a.Path]
		if !ok {
			return report, fmt.Errorf("missing archive payload %q", a.Path)
		}
		if int64(len(data)) != a.Size {
			return report, fmt.Errorf("asset %q size mismatch", a.Path)
		}
		payloadBytes += a.Size
		if payloadBytes > MaxExpandedBytes {
			return report, errors.New("asset payload bytes exceed expanded budget")
		}
		sum := sha256.Sum256(data)
		if !strings.EqualFold(a.SHA256, hex.EncodeToString(sum[:])) {
			return report, fmt.Errorf("asset %q digest mismatch", a.Path)
		}
		if err := validateAssetPayload(a, data); err != nil {
			return report, err
		}
	}
	archiveSum := sha256.Sum256(raw)
	report.Manifest = m
	report.ArchiveSHA256 = hex.EncodeToString(archiveSum[:])
	report.ExpandedBytes = payloadBytes
	return report, nil
}

func signingBytes(m Manifest) ([]byte, error) {
	m.Signature.Value = ""
	return json.Marshal(m)
}

func validateManifestShape(m Manifest, requireSignature bool) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", m.SchemaVersion)
	}
	if !safeToken(m.BundleID, 96) || !safeToken(m.Version, 64) {
		return errors.New("bundle_id or version is invalid")
	}
	if m.MinAssetABI <= 0 {
		return errors.New("min_asset_abi must be positive")
	}
	if len(m.Targets) == 0 || len(m.Targets) > 16 {
		return errors.New("targets count is outside allowed range")
	}
	for _, t := range m.Targets {
		if !(t.Board == "*" || safeToken(t.Board, 64)) || t.Width <= 0 || t.Height <= 0 || t.Width > MaxImageDimension || t.Height > MaxImageDimension {
			return errors.New("target selector is invalid")
		}
	}
	if len(m.Assets) == 0 || len(m.Assets) > MaxEntries {
		return errors.New("assets count is outside allowed range")
	}
	for _, a := range m.Assets {
		if !safeRole(a.Role) || !allowedTypes[a.Type] || !safeAssetPath(a.Path) {
			return fmt.Errorf("asset metadata is invalid for %q", a.Path)
		}
		if a.Size <= 0 || a.Size > MaxAssetBytes {
			return fmt.Errorf("asset %q size is outside allowed budget", a.Path)
		}
		b, err := hex.DecodeString(a.SHA256)
		if err != nil || len(b) != sha256.Size {
			return fmt.Errorf("asset %q sha256 must be 64 hex characters", a.Path)
		}
		if strings.TrimSpace(a.License.Source) == "" || strings.TrimSpace(a.License.License) == "" {
			return fmt.Errorf("asset %q requires source and license provenance", a.Path)
		}
		for _, lang := range a.Languages {
			if lang != "en" && lang != "vi" {
				return fmt.Errorf("asset %q declares unsupported language %q", a.Path, lang)
			}
		}
		if a.Type != "font_ttf" && len(a.Languages) != 0 {
			return fmt.Errorf("asset %q may declare languages only for font_ttf", a.Path)
		}
	}
	if m.Signature.Algorithm != "ed25519" || !safeToken(m.Signature.KeyID, 64) {
		return errors.New("signature metadata must use ed25519 and a valid key_id")
	}
	if requireSignature && m.Signature.Value == "" {
		return errors.New("signature value is required")
	}
	return nil
}

func compatible(targets []Target, opts ValidateOptions) bool {
	if opts.AssetABI <= 0 || opts.Width <= 0 || opts.Height <= 0 || strings.TrimSpace(opts.Board) == "" {
		return false
	}
	for _, t := range targets {
		if (t.Board == "*" || t.Board == opts.Board) && t.Width == opts.Width && t.Height == opts.Height {
			return true
		}
	}
	return false
}

func validateAssetPayload(a Asset, data []byte) error {
	switch a.Type {
	case "image_png":
		w, h, err := pngDimensions(data)
		if err != nil {
			return fmt.Errorf("asset %q: %w", a.Path, err)
		}
		if w > MaxImageDimension || h > MaxImageDimension || int64(w)*int64(h) > MaxImagePixels {
			return fmt.Errorf("asset %q image dimensions exceed budget", a.Path)
		}
		if a.Width != w || a.Height != h {
			return fmt.Errorf("asset %q image dimensions do not match manifest", a.Path)
		}
	case "theme_json":
		if a.Width != 0 || a.Height != 0 {
			return fmt.Errorf("asset %q theme must not declare dimensions", a.Path)
		}
		var v any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("asset %q contains invalid theme JSON", a.Path)
		}
		if dec.Decode(&struct{}{}) != io.EOF {
			return fmt.Errorf("asset %q theme must contain one JSON value", a.Path)
		}
		if err := rejectRuntimeURLs(v); err != nil {
			return fmt.Errorf("asset %q: %w", a.Path, err)
		}
	case "font_ttf":
		if a.Width != 0 || a.Height != 0 {
			return fmt.Errorf("asset %q font must not declare dimensions", a.Path)
		}
		for _, lang := range a.Languages {
			for _, r := range requiredRunes(lang) {
				ok, err := ttfHasRune(data, r)
				if err != nil {
					return fmt.Errorf("asset %q invalid TTF cmap: %w", a.Path, err)
				}
				if !ok {
					return fmt.Errorf("asset %q is missing required %s glyph U+%04X", a.Path, lang, r)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported asset type %q", a.Type)
	}
	return nil
}

func pngDimensions(data []byte) (int, int, error) {
	if len(data) < 24 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) || string(data[12:16]) != "IHDR" {
		return 0, 0, errors.New("invalid PNG header")
	}
	w := int(binary.BigEndian.Uint32(data[16:20]))
	h := int(binary.BigEndian.Uint32(data[20:24]))
	if w <= 0 || h <= 0 {
		return 0, 0, errors.New("PNG dimensions must be positive")
	}
	return w, h, nil
}

func rejectRuntimeURLs(v any) error {
	switch x := v.(type) {
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "javascript:") || strings.HasPrefix(s, "data:") {
			return errors.New("runtime URL/script reference is not allowed")
		}
	case []any:
		for _, item := range x {
			if err := rejectRuntimeURLs(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range x {
			if err := rejectRuntimeURLs(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func safeArchivePath(name string) bool {
	if name == "manifest.json" {
		return true
	}
	return safeAssetPath(name)
}

func safeAssetPath(name string) bool {
	if name == "" || len(name) > MaxPathBytes || !utf8.ValidString(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return false
	}
	if path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || strings.Contains(name, "/./") {
		return false
	}
	return strings.HasPrefix(name, "assets/") && len(name) > len("assets/")
}

func safeToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func safeRole(s string) bool {
	return safeToken(s, 96) && !strings.HasPrefix(s, ".") && !strings.HasSuffix(s, ".")
}

func requiredRunes(lang string) []rune {
	switch lang {
	case "en":
		return []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	case "vi":
		return []rune("AĂÂBCDĐEÊGHIKLMNOÔƠPQRSTUƯVXYaăâbcdđeêghiklmnoôơpqrstuưvxyÀÁẢÃẠẰẮẲẴẶẦẤẨẪẬÈÉẺẼẸỀẾỂỄỆÌÍỈĨỊÒÓỎÕỌỒỐỔỖỘỜỚỞỠỢÙÚỦŨỤỪỨỬỮỰỲÝỶỸỴàáảãạằắẳẵặầấẩẫậèéẻẽẹềếểễệìíỉĩịòóỏõọồốổỗộờớởỡợùúủũụừứửữựỳýỷỹỵ")
	default:
		return nil
	}
}

func ttfHasRune(data []byte, r rune) (bool, error) {
	if len(data) < 12 {
		return false, errors.New("truncated sfnt header")
	}
	numTables := int(binary.BigEndian.Uint16(data[4:6]))
	if numTables <= 0 || 12+numTables*16 > len(data) {
		return false, errors.New("invalid sfnt table directory")
	}
	var cmap []byte
	for i := 0; i < numTables; i++ {
		off := 12 + i*16
		if string(data[off:off+4]) != "cmap" {
			continue
		}
		start := int(binary.BigEndian.Uint32(data[off+8 : off+12]))
		length := int(binary.BigEndian.Uint32(data[off+12 : off+16]))
		if start < 0 || length < 4 || start > len(data) || length > len(data)-start {
			return false, errors.New("invalid cmap table bounds")
		}
		cmap = data[start : start+length]
		break
	}
	if len(cmap) < 4 {
		return false, errors.New("missing cmap table")
	}
	n := int(binary.BigEndian.Uint16(cmap[2:4]))
	if 4+n*8 > len(cmap) {
		return false, errors.New("invalid cmap encoding records")
	}
	for i := 0; i < n; i++ {
		rec := 4 + i*8
		off := int(binary.BigEndian.Uint32(cmap[rec+4 : rec+8]))
		if off < 0 || off+2 > len(cmap) {
			continue
		}
		format := binary.BigEndian.Uint16(cmap[off : off+2])
		var ok bool
		var err error
		switch format {
		case 4:
			ok, err = cmap4HasRune(cmap[off:], r)
		case 12:
			ok, err = cmap12HasRune(cmap[off:], r)
		default:
			continue
		}
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func cmap12HasRune(b []byte, r rune) (bool, error) {
	if len(b) < 16 {
		return false, errors.New("truncated cmap format 12")
	}
	length := int(binary.BigEndian.Uint32(b[4:8]))
	groups := int(binary.BigEndian.Uint32(b[12:16]))
	if length < 16 || length > len(b) || groups > (length-16)/12 {
		return false, errors.New("invalid cmap format 12 bounds")
	}
	cp := uint32(r)
	lo, hi := 0, groups
	for lo < hi {
		mid := lo + (hi-lo)/2
		off := 16 + mid*12
		start := binary.BigEndian.Uint32(b[off : off+4])
		end := binary.BigEndian.Uint32(b[off+4 : off+8])
		if cp < start {
			hi = mid
		} else if cp > end {
			lo = mid + 1
		} else {
			return true, nil
		}
	}
	return false, nil
}

func cmap4HasRune(b []byte, r rune) (bool, error) {
	if r > 0xffff {
		return false, nil
	}
	if len(b) < 16 {
		return false, errors.New("truncated cmap format 4")
	}
	length := int(binary.BigEndian.Uint16(b[2:4]))
	segCount := int(binary.BigEndian.Uint16(b[6:8])) / 2
	if length > len(b) || segCount <= 0 {
		return false, errors.New("invalid cmap format 4 bounds")
	}
	endOff := 14
	startOff := endOff + segCount*2 + 2
	deltaOff := startOff + segCount*2
	rangeOff := deltaOff + segCount*2
	if rangeOff+segCount*2 > length {
		return false, errors.New("invalid cmap format 4 arrays")
	}
	cp := uint16(r)
	for i := 0; i < segCount; i++ {
		end := binary.BigEndian.Uint16(b[endOff+i*2 : endOff+i*2+2])
		start := binary.BigEndian.Uint16(b[startOff+i*2 : startOff+i*2+2])
		if cp < start || cp > end {
			continue
		}
		delta := binary.BigEndian.Uint16(b[deltaOff+i*2 : deltaOff+i*2+2])
		ro := binary.BigEndian.Uint16(b[rangeOff+i*2 : rangeOff+i*2+2])
		if ro == 0 {
			return uint16(uint32(cp)+uint32(delta)) != 0, nil
		}
		glyphPos := rangeOff + i*2 + int(ro) + int(cp-start)*2
		if glyphPos+2 > length {
			return false, errors.New("invalid cmap format 4 glyph index")
		}
		glyph := binary.BigEndian.Uint16(b[glyphPos : glyphPos+2])
		if glyph == 0 {
			return false, nil
		}
		return uint16(uint32(glyph)+uint32(delta)) != 0, nil
	}
	return false, nil
}
