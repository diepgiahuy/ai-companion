package speech

import (
	"testing"
)

func TestCatalogCreationAndValidation(t *testing.T) {
	_, err := NewCatalog([]Voice{
		{Key: "", Provider: "edge"},
	})
	if err == nil {
		t.Fatal("expected error for empty voice key")
	}

	_, err = NewCatalog([]Voice{
		{Key: "default", Provider: ""},
	})
	if err == nil {
		t.Fatal("expected error for empty voice provider")
	}

	catalog, err := NewCatalog([]Voice{
		{
			Key:           "hoaimy",
			Provider:      "edge-tts",
			ProviderVoice: "vi-VN-HoaiMyNeural",
			Locales:       []string{"vi-VN"},
			Streaming:     false,
			SampleRate:    24000,
		},
		{
			Key:           "jenny",
			Provider:      "edge-tts",
			ProviderVoice: "en-US-JennyNeural",
			Locales:       []string{"en-US", "en-GB"},
			Streaming:     false,
			SampleRate:    24000,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating catalog: %v", err)
	}

	// Exact locale match
	voice, ok := catalog.Resolve("hoaimy", "vi-VN")
	if !ok || voice.ProviderVoice != "vi-VN-HoaiMyNeural" {
		t.Fatalf("resolve exact failed: ok=%v voice=%+v", ok, voice)
	}

	// Prefix / language-only match
	voice, ok = catalog.Resolve("hoaimy", "vi")
	if !ok || voice.ProviderVoice != "vi-VN-HoaiMyNeural" {
		t.Fatalf("resolve prefix failed: ok=%v voice=%+v", ok, voice)
	}

	// Unmatched locale
	_, ok = catalog.Resolve("hoaimy", "ja-JP")
	if ok {
		t.Fatal("expected resolve to fail for non-matching locale")
	}

	// Non-existent key
	_, ok = catalog.Resolve("unknown", "vi-VN")
	if ok {
		t.Fatal("expected resolve to fail for unknown key")
	}
}

func TestValidLocale(t *testing.T) {
	valid := []string{"vi-VN", "en-US", "zh-CN", "ja", "fr-FR", "de"}
	for _, l := range valid {
		if !ValidLocale(l) {
			t.Errorf("ValidLocale(%q) = false, want true", l)
		}
	}

	invalid := []string{"", "1", "12", "toolongtag", "a", "-VN"}
	for _, l := range invalid {
		if ValidLocale(l) {
			t.Errorf("ValidLocale(%q) = true, want false", l)
		}
	}
}
