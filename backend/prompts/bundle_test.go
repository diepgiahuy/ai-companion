package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultBundleComposesOnlySelectedDomainBlocks(t *testing.T) {
	bundle, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := bundle.Render(RenderInput{
		Locale:      "vi-VN",
		CurrentTime: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		Timezone:    "Asia/Ho_Chi_Minh",
		Packs:       []string{"expense"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Text, "Financial and budget state") {
		t.Fatal("finance block was not composed")
	}
	if strings.Contains(rendered.Text, "Market, weather") {
		t.Fatal("unselected external-data block leaked into prompt")
	}
	if rendered.ID != "companion" || rendered.Version != "4.0.0" || len(rendered.Fingerprint) != 64 {
		t.Fatalf("unexpected metadata: %+v", rendered)
	}
}

func TestDirectoryOverrideChangesPersonaWithoutRecompile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "domains"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"custom","version":"1","base":["base.md"],"packs":{}}`
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "base.md"), []byte("Persona={{.Persona}} Locale={{.Locale}}"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := LoadDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := bundle.Render(RenderInput{Locale: "en-US", Persona: "formal"})
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Text != "Persona=formal Locale=en-US" {
		t.Fatalf("unexpected custom prompt: %q", rendered.Text)
	}
}

func TestBundleRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"id":"x","version":"1","base":["../secret"],"packs":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDirectory(root); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
