package pgstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	evalharness "companion-server/eval"
	"companion-server/internal/contextengine"
	"companion-server/internal/memory"
)

func TestPostgresPersonalRetrievalEvaluationIntegration(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := store.VerifySchema(ctx); err != nil {
		t.Fatalf("schema verification failed: %v", err)
	}

	f, err := os.Open("../../eval/personal_retrieval_corpus.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	scenarios, hash, err := evalharness.LoadPersonalRetrievalCorpus(f)
	if err != nil {
		t.Fatalf("load retrieval corpus: %v", err)
	}
	if hash == "" {
		t.Fatal("empty corpus hash")
	}

	recDir := filepath.Join(t.TempDir(), "recordings")
	baseTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	mem := memory.New(store, memory.HashEmbedding{Dimensions: 32})
	router := contextengine.New(nil)

	deps := evalharness.RetrievalDependencies{
		Store:         store,
		Memory:        mem,
		Router:        router,
		RecordingsDir: recDir,
		Now:           func() time.Time { return baseTime },
	}

	report, err := evalharness.RunPersonalRetrievalEvaluation(ctx, scenarios, deps)
	if err != nil {
		t.Fatalf("run retrieval evaluation against postgres: %v", err)
	}

	if report.TotalCases != len(scenarios) {
		t.Fatalf("expected %d cases, got %d", len(scenarios), report.TotalCases)
	}
	if report.PassedCases != report.TotalCases {
		t.Errorf("postgres retrieval evaluation pass rate = %.2f (%d/%d passed)", report.PassRate, report.PassedCases, report.TotalCases)
		for _, r := range report.Results {
			if !r.Passed {
				t.Errorf("FAILED case %s (%s, %s): %s [FailureType: %s]", r.CaseID, r.Language, r.Category, r.Reason, r.FailureType)
			}
		}
	}
}
