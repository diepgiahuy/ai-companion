package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/contextengine"
	"companion-server/internal/memory"
	"companion-server/internal/providers/resources"
	"companion-server/internal/providers/tools"
	"companion-server/internal/store"
)

func TestPersonalRetrievalEvaluationCorpus(t *testing.T) {
	f, err := os.Open("personal_retrieval_corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scenarios, hash, err := LoadPersonalRetrievalCorpus(f)
	if err != nil {
		t.Fatalf("load retrieval corpus: %v", err)
	}
	if len(scenarios) < 40 {
		t.Fatalf("retrieval corpus too small: got %d, want at least 40", len(scenarios))
	}
	if hash == "" {
		t.Fatal("expected non-empty corpus SHA256")
	}

	data, err := store.Open(filepath.Join(t.TempDir(), "retrieval_eval.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	recDir := filepath.Join(t.TempDir(), "recordings")
	baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	mem := memory.New(data, memory.HashEmbedding{Dimensions: 32})

	toolRegistry := capability.NewToolRegistry()
	_ = tools.RegisterNative(toolRegistry, tools.NativeDependencies{
		Store:         data,
		RecordingsDir: recDir,
		Now:           func() time.Time { return baseTime },
	})
	_ = tools.RegisterPlatform(toolRegistry, tools.PlatformDependencies{
		Memory: mem,
		Now:    func() time.Time { return baseTime },
	})

	resourceRegistry := capability.NewResourceRegistry()
	_ = resourceRegistry.Register(resources.NewNative(data, nil, time.UTC))

	router := contextengine.New(resourceRegistry)

	deps := RetrievalDependencies{
		Store:         data,
		Memory:        mem,
		Registry:      toolRegistry,
		Router:        router,
		Resources:     resourceRegistry,
		RecordingsDir: recDir,
		Now:           func() time.Time { return baseTime },
	}

	report, err := RunPersonalRetrievalEvaluation(context.Background(), scenarios, deps)
	if err != nil {
		t.Fatalf("run retrieval evaluation: %v", err)
	}

	if report.TotalCases != len(scenarios) {
		t.Fatalf("report total cases = %d, want %d", report.TotalCases, len(scenarios))
	}
	if report.PassedCases != report.TotalCases {
		t.Errorf("retrieval evaluation pass rate = %.2f (%d/%d passed)", report.PassRate, report.PassedCases, report.TotalCases)
		for _, r := range report.Results {
			if !r.Passed {
				t.Errorf("FAILED case %s (%s, %s): %s [FailureType: %s]", r.CaseID, r.Language, r.Category, r.Reason, r.FailureType)
			}
		}
	}

	// Verify all language breakdowns exist and pass 100%
	for _, lang := range []string{"vi", "en", "mixed"} {
		st, ok := report.LanguageBreakdown[lang]
		if !ok || st.Total == 0 {
			t.Errorf("missing language stats for %s", lang)
		} else if st.Passed != st.Total {
			t.Errorf("language %s passed %d/%d (rate: %.2f)", lang, st.Passed, st.Total, st.Rate)
		}
	}

	// Verify all category breakdowns exist and pass 100%
	for _, cat := range []string{"single_domain", "cross_domain", "temporal", "empty_result", "deletion_recall", "superseded_memory", "owner_isolation", "read_only_ambiguity", "limit_boundary"} {
		st, ok := report.CategoryBreakdown[cat]
		if !ok || st.Total == 0 {
			t.Errorf("missing category stats for %s", cat)
		} else if st.Passed != st.Total {
			t.Errorf("category %s passed %d/%d (rate: %.2f)", cat, st.Passed, st.Total, st.Rate)
		}
	}

	// Verify all domain breakdowns exist and pass 100%
	for _, d := range []string{"expense", "budget", "saving", "note", "journal", "schedule", "voice", "memory"} {
		st, ok := report.DomainBreakdown[d]
		if !ok || st.Total == 0 {
			t.Errorf("missing domain stats for %s", d)
		} else if st.Passed != st.Total {
			t.Errorf("domain %s passed %d/%d (rate: %.2f)", d, st.Passed, st.Total, st.Rate)
		}
	}
}
