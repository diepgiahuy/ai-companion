package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

//go:embed companion/v4/*.md companion/v4/domains/*.md companion/v4/manifest.json
var defaults embed.FS

type Manifest struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Base    []string          `json:"base"`
	Packs   map[string]string `json:"packs"`
}

type RenderInput struct {
	Locale      string
	CurrentTime time.Time
	Timezone    string
	Persona     string
	Packs       []string
}

type Rendered struct {
	Text        string
	ID          string
	Version     string
	Fingerprint string
}

type Bundle struct {
	manifest Manifest
	files    map[string][]byte
}

// LoadDefault returns the immutable prompt bundle shipped with the binary.
// Production can use LoadDirectory to override it without recompiling Go code;
// the rendered prompt fingerprint is exposed for traces/evaluation/rollback.
func LoadDefault() (*Bundle, error) {
	root, err := fs.Sub(defaults, "companion/v4")
	if err != nil {
		return nil, err
	}
	return loadFS(root)
}

func LoadDirectory(dir string) (*Bundle, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("prompt directory is required")
	}
	return loadFS(os.DirFS(filepath.Clean(dir)))
}

func loadFS(root fs.FS) (*Bundle, error) {
	raw, err := fs.ReadFile(root, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read prompt manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode prompt manifest: %w", err)
	}
	if strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.Version) == "" {
		return nil, fmt.Errorf("prompt manifest id/version are required")
	}

	paths := append([]string(nil), manifest.Base...)
	for _, path := range manifest.Packs {
		paths = append(paths, path)
	}
	files := make(map[string][]byte, len(paths))
	for _, path := range paths {
		if err := validateRelativePath(path); err != nil {
			return nil, err
		}
		if _, exists := files[path]; exists {
			continue
		}
		content, err := fs.ReadFile(root, path)
		if err != nil {
			return nil, fmt.Errorf("read prompt block %q: %w", path, err)
		}
		files[path] = content
	}
	return &Bundle{manifest: manifest, files: files}, nil
}

func validateRelativePath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return fmt.Errorf("invalid prompt block path %q", path)
	}
	return nil
}

func (b *Bundle) Render(input RenderInput) (Rendered, error) {
	if b == nil {
		return Rendered{}, fmt.Errorf("prompt bundle is nil")
	}
	packs := uniqueSorted(input.Packs)
	paths := append([]string(nil), b.manifest.Base...)
	for _, pack := range packs {
		if path := strings.TrimSpace(b.manifest.Packs[pack]); path != "" {
			paths = append(paths, path)
		}
	}

	data := struct {
		Locale      string
		CurrentTime string
		Timezone    string
		Persona     string
	}{
		Locale:      value(input.Locale, "vi-VN"),
		CurrentTime: input.CurrentTime.Format(time.RFC3339),
		Timezone:    input.Timezone,
		Persona:     strings.TrimSpace(input.Persona),
	}

	var out strings.Builder
	hash := sha256.New()
	_, _ = hash.Write([]byte(b.manifest.ID + "\x00" + b.manifest.Version + "\x00"))
	for _, path := range paths {
		content, ok := b.files[path]
		if !ok {
			return Rendered{}, fmt.Errorf("prompt block %q is not loaded", path)
		}
		tmpl, err := template.New(path).Option("missingkey=error").Parse(string(content))
		if err != nil {
			return Rendered{}, fmt.Errorf("parse prompt block %q: %w", path, err)
		}
		var block strings.Builder
		if err := tmpl.Execute(&block, data); err != nil {
			return Rendered{}, fmt.Errorf("render prompt block %q: %w", path, err)
		}
		text := strings.TrimSpace(block.String())
		if text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(text)
		_, _ = hash.Write([]byte(path + "\x00" + text + "\x00"))
	}
	if out.Len() == 0 {
		return Rendered{}, fmt.Errorf("rendered prompt is empty")
	}
	return Rendered{
		Text:        out.String(),
		ID:          b.manifest.ID,
		Version:     b.manifest.Version,
		Fingerprint: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item != "" {
			seen[item] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for item := range seen {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func value(current, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return strings.TrimSpace(current)
	}
	return fallback
}
