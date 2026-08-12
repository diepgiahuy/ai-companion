package agent

import (
	"context"
	"fmt"
	"testing"
)

type testEmbedder struct {
	vectors map[string][]float32
}

func (e testEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	v, ok := e.vectors[text]
	if !ok {
		return nil, fmt.Errorf("missing test vector for %q", text)
	}
	return v, nil
}

func TestStaticModelSelectorDoesNotInspectText(t *testing.T) {
	s := StaticModelSelector{Model: "fast"}
	if got := s.Select(context.Background(), "phân tích cực kỳ phức tạp"); got != "fast" {
		t.Fatalf("static selector changed model based on text: %q", got)
	}
}

func TestSemanticModelSelectorUsesEmbeddingSimilarity(t *testing.T) {
	embedder := testEmbedder{vectors: map[string][]float32{
		"quick":   {1, 0},
		"reason":  {0, 1},
		"question": {0.05, 0.99},
	}}
	s, err := NewSemanticModelSelector(context.Background(), embedder, "fast", "reasoning", SemanticRouteExamples{
		Fast:      []string{"quick"},
		Reasoning: []string{"reason"},
		Margin:    0.05,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Select(context.Background(), "question"); got != "reasoning" {
		t.Fatalf("expected reasoning model, got %q", got)
	}
}
