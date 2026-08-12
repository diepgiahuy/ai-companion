package agent

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type EmbeddingProvider interface {
	Embed(context.Context, string) ([]float32, error)
}

type SemanticRouteExamples struct {
	Fast      []string `json:"fast"`
	Reasoning []string `json:"reasoning"`
	Margin    float64  `json:"margin"`
}

type SemanticModelSelector struct {
	embedder      EmbeddingProvider
	fastModel     string
	reasoningModel string
	fastVectors   [][]float32
	reasonVectors [][]float32
	margin        float64
}

// NewSemanticModelSelector builds semantic prototypes once at startup. The
// examples are configuration/eval data rather than keyword rules compiled into
// the binary. If semantic routing is selected in production and prototype
// embedding fails, startup should fail instead of silently changing routing.
func NewSemanticModelSelector(ctx context.Context, embedder EmbeddingProvider, fastModel, reasoningModel string, examples SemanticRouteExamples) (*SemanticModelSelector, error) {
	if embedder == nil {
		return nil, fmt.Errorf("semantic router requires an embedding provider")
	}
	fastModel = strings.TrimSpace(fastModel)
	reasoningModel = strings.TrimSpace(reasoningModel)
	if fastModel == "" {
		return nil, fmt.Errorf("semantic router fast model is required")
	}
	if reasoningModel == "" {
		return nil, fmt.Errorf("semantic router reasoning model is required")
	}
	if len(examples.Fast) == 0 || len(examples.Reasoning) == 0 {
		return nil, fmt.Errorf("semantic router requires fast and reasoning examples")
	}
	if examples.Margin < 0 || examples.Margin > 1 {
		return nil, fmt.Errorf("semantic router margin must be between 0 and 1")
	}

	selector := &SemanticModelSelector{
		embedder: embedder,
		fastModel: fastModel,
		reasoningModel: reasoningModel,
		margin: examples.Margin,
	}
	var err error
	selector.fastVectors, err = embedExamples(ctx, embedder, examples.Fast)
	if err != nil {
		return nil, fmt.Errorf("embed fast routing examples: %w", err)
	}
	selector.reasonVectors, err = embedExamples(ctx, embedder, examples.Reasoning)
	if err != nil {
		return nil, fmt.Errorf("embed reasoning routing examples: %w", err)
	}
	return selector, nil
}

func (s *SemanticModelSelector) Select(ctx context.Context, query string) string {
	if s == nil || s.embedder == nil || strings.TrimSpace(query) == "" {
		if s != nil {
			return s.fastModel
		}
		return ""
	}
	vector, err := s.embedder.Embed(ctx, query)
	if err != nil {
		// Routing is an optimization, not a correctness boundary. A transient
		// classifier failure degrades to the configured fast model and is expected
		// to be surfaced by caller metrics rather than failing the user turn.
		return s.fastModel
	}
	fast := maxSimilarity(vector, s.fastVectors)
	reasoning := maxSimilarity(vector, s.reasonVectors)
	if reasoning >= fast+s.margin {
		return s.reasoningModel
	}
	return s.fastModel
}

func embedExamples(ctx context.Context, embedder EmbeddingProvider, examples []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(examples))
	for _, example := range examples {
		example = strings.TrimSpace(example)
		if example == "" {
			continue
		}
		vector, err := embedder.Embed(ctx, example)
		if err != nil {
			return nil, err
		}
		if len(vector) == 0 {
			return nil, fmt.Errorf("embedding provider returned empty vector")
		}
		vectors = append(vectors, vector)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("no usable routing examples")
	}
	return vectors, nil
}

func maxSimilarity(query []float32, candidates [][]float32) float64 {
	best := -1.0
	for _, candidate := range candidates {
		if score := cosineSimilarity(query, candidate); score > best {
			best = score
		}
	}
	return best
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}
	var dot, aa, bb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
	}
	if aa == 0 || bb == 0 {
		return -1
	}
	return dot / math.Sqrt(aa*bb)
}
