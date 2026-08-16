package assetbundle

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

func validateAssetContent(a Asset, data []byte, limits Limits) error {
	if !safeRole(a.Role) {
		return fmt.Errorf("%w: invalid asset role", ErrInvalidBundle)
	}
	if a.FallbackRole != "" && !safeRole(a.FallbackRole) {
		return fmt.Errorf("%w: invalid fallback role", ErrInvalidBundle)
	}
	if err := validatePath(a.Path, limits.MaxPathBytes); err != nil {
		return err
	}
	if !strings.HasPrefix(a.Path, "assets/") {
		return fmt.Errorf("%w: asset path must be under assets/", ErrInvalidBundle)
	}
	if a.Size <= 0 || a.Size > limits.MaxAssetBytes {
		return fmt.Errorf("%w: invalid asset size for %q", ErrInvalidBundle, a.Path)
	}
	if len(a.SHA256) != 64 || a.SHA256 != strings.ToLower(a.SHA256) {
		return fmt.Errorf("%w: invalid sha256 for %q", ErrInvalidBundle, a.Path)
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return fmt.Errorf("%w: invalid sha256 for %q", ErrInvalidBundle, a.Path)
	}
	if err := validateLicense(a.License); err != nil {
		return fmt.Errorf("%w: asset %q license: %v", ErrInvalidBundle, a.Path, err)
	}
	switch a.Type {
	case "theme/json":
		if path.Ext(a.Path) != ".json" {
			return fmt.Errorf("%w: theme asset must use .json", ErrInvalidBundle)
		}
		type themeDocument struct {
			Palette map[string]string `json:"palette"`
		}
		var v themeDocument
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%w: invalid theme JSON for %q", ErrInvalidBundle, a.Path)
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			return fmt.Errorf("%w: trailing theme JSON for %q", ErrInvalidBundle, a.Path)
		}
		if len(v.Palette) == 0 || len(v.Palette) > 32 {
			return fmt.Errorf("%w: theme palette size invalid for %q", ErrInvalidBundle, a.Path)
		}
		for key, value := range v.Palette {
			if !safeID(key, 48) || !hexColor(value) {
				return fmt.Errorf("%w: invalid theme palette entry %q", ErrInvalidBundle, key)
			}
		}
		if a.Width != 0 || a.Height != 0 || len(a.GlyphProfiles) > 0 {
			return fmt.Errorf("%w: theme metadata mismatch", ErrInvalidBundle)
		}
	case "image/png":
		if path.Ext(a.Path) != ".png" {
			return fmt.Errorf("%w: PNG asset must use .png", ErrInvalidBundle)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%w: invalid PNG %q", ErrInvalidBundle, a.Path)
		}
		if cfg.Width != a.Width || cfg.Height != a.Height || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > limits.MaxImageWidth || cfg.Height > limits.MaxImageHeight {
			return fmt.Errorf("%w: image dimensions invalid for %q", ErrInvalidBundle, a.Path)
		}
		// DecodeConfig only validates enough bytes to read image metadata. Decode the
		// complete bounded image after checking dimensions so a signed/trusted manifest
		// cannot smuggle a truncated or corrupt pixel stream to the later renderer.
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			return fmt.Errorf("%w: corrupt PNG payload %q: %v", ErrInvalidBundle, a.Path, err)
		}
		if len(a.GlyphProfiles) > 0 {
			return fmt.Errorf("%w: image cannot declare glyph profiles", ErrInvalidBundle)
		}
	case "font/ttf":
		if path.Ext(a.Path) != ".ttf" {
			return fmt.Errorf("%w: font asset must use .ttf", ErrInvalidBundle)
		}
		if a.Width != 0 || a.Height != 0 || len(a.GlyphProfiles) == 0 {
			return fmt.Errorf("%w: font metadata invalid for %q", ErrInvalidBundle, a.Path)
		}
		for _, profile := range a.GlyphProfiles {
			runes, ok := glyphProfile(profile)
			if !ok {
				return fmt.Errorf("%w: unsupported glyph profile %q", ErrInvalidBundle, profile)
			}
			if err := fontCovers(data, runes); err != nil {
				return fmt.Errorf("%w: font %q profile %s: %v", ErrInvalidBundle, a.Path, profile, err)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported asset type %q", ErrInvalidBundle, a.Type)
	}
	return nil
}

func safeRole(s string) bool {
	if s == "" || len(s) > 96 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func validateLicense(l License) error {
	if !safeID(l.ID, 64) || len(l.Source) == 0 || len(l.Source) > 256 || len(l.Attribution) > 512 {
		return fmt.Errorf("license id/source required and bounded")
	}
	if strings.HasPrefix(l.Source, "generated:") {
		return nil
	}
	u, err := url.Parse(l.Source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("license source must be HTTPS provenance or generated:test metadata")
	}
	return nil
}

func hexColor(v string) bool {
	if len(v) != 7 && len(v) != 9 || !strings.HasPrefix(v, "#") {
		return false
	}
	_, err := hex.DecodeString(v[1:])
	return err == nil
}

func validatePath(p string, max int) error {
	if p == "" || len(p) > max || !utf8.ValidString(p) || strings.ContainsRune(p, '\\') || strings.ContainsRune(p, '\x00') || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return fmt.Errorf("%w: unsafe path %q", ErrInvalidBundle, p)
	}
	return nil
}
func readBounded(f *zip.File, max int64) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: required archive entry missing", ErrInvalidBundle)
	}
	if f.UncompressedSize64 > uint64(max) {
		return nil, fmt.Errorf("%w: entry %q exceeds limit", ErrInvalidBundle, f.Name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open %q: %v", ErrInvalidBundle, f.Name, err)
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, fmt.Errorf("%w: read %q exceeds/corrupt", ErrInvalidBundle, f.Name)
	}
	return b, nil
}
func safeID(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}
func safeVersion(s string) bool { return safeID(s, 64) }

func glyphProfile(name string) ([]rune, bool) {
	switch name {
	case "en":
		return []rune(englishGlyphs), true
	case "vi":
		return []rune(vietnameseGlyphs), true
	default:
		return nil, false
	}
}

const englishGlyphs = " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"
const vietnameseGlyphs = englishGlyphs + "ĂÂÊÔƠƯĐăâêôơưđÁÀẢÃẠÉÈẺẼẸÍÌỈĨỊÓÒỎÕỌÚÙỦŨỤÝỲỶỸỴáàảãạéèẻẽẹíìỉĩịóòỏõọúùủũụýỳỷỹỵẤẦẨẪẬẮẰẲẴẶẾỀỂỄỆỐỒỔỖỘỚỜỞỠỢỨỪỬỮỰấầẩẫậắằẳẵặếềểễệốồổỗộớờởỡợứừửữự"