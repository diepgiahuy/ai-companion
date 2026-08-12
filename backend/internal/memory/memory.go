package memory

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"time"
)

type Kind string

const (
	Semantic Kind = "semantic"
	Episodic Kind = "episodic"
	Temporal Kind = "temporal"
)

type Item struct {
	ID         int64      `json:"id"`
	UserID     string     `json:"user_id,omitempty"`
	Key        string     `json:"key"`
	Kind       Kind       `json:"kind"`
	Value      string     `json:"value"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	Source     string     `json:"source"`
	Confidence float64    `json:"confidence"`
	Embedding  []float32  `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
}
type ScoredItem struct {
	Item  Item    `json:"memory"`
	Score float64 `json:"score"`
}

type Repository interface {
	UpsertMemory(context.Context, Item) (Item, error)
	CurrentMemories(context.Context, string, time.Time, int) ([]Item, error)
	ForgetMemory(context.Context, string, string) error
}
type EmbeddingProvider interface {
	Embed(context.Context, string) ([]float32, error)
}

type VectorHit struct {
	ID    int64
	Score float64
}
type VectorStore interface {
	UpsertVector(context.Context, string, int64, []float32) error
	SearchVectors(context.Context, string, []float32, int) ([]VectorHit, error)
	DeleteVector(context.Context, string, int64) error
}

// HashEmbedding is deterministic/offline and intentionally replaceable. It is
// useful for tests/POC, while production can use a multilingual embedding model.
type HashEmbedding struct{ Dimensions int }

func (h HashEmbedding) Embed(_ context.Context, text string) ([]float32, error) {
	d := h.Dimensions
	if d <= 0 {
		d = 96
	}
	v := make([]float32, d)
	tokens := tokenize(text)
	for _, t := range tokens {
		sum := sha256.Sum256([]byte(t))
		idx := int(binary.LittleEndian.Uint32(sum[:4]) % uint32(d))
		sign := float32(1)
		if sum[4]&1 == 1 {
			sign = -1
		}
		v[idx] += sign
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n > 0 {
		z := float32(math.Sqrt(n))
		for i := range v {
			v[i] /= z
		}
	}
	return v, nil
}

type Service struct {
	repo    Repository
	vectors VectorStore
	embed   EmbeddingProvider
	now     func() time.Time
}

func New(repo Repository, embed EmbeddingProvider) *Service {
	var vectors VectorStore
	if v, ok := repo.(VectorStore); ok {
		vectors = v
	}
	return NewWithVector(repo, vectors, embed)
}
func NewWithVector(repo Repository, vectors VectorStore, embed EmbeddingProvider) *Service {
	if embed == nil {
		embed = HashEmbedding{Dimensions: 96}
	}
	return &Service{repo: repo, vectors: vectors, embed: embed, now: time.Now}
}
func (s *Service) Remember(ctx context.Context, user, key string, kind Kind, value, source string, confidence float64, validFrom time.Time) (Item, error) {
	if validFrom.IsZero() {
		validFrom = s.now()
	}
	if confidence <= 0 {
		confidence = 1
	}
	if source == "" {
		source = "user"
	}
	emb, err := s.embed.Embed(ctx, key+" "+value)
	if err != nil {
		return Item{}, err
	}
	m, err := s.repo.UpsertMemory(ctx, Item{UserID: user, Key: strings.TrimSpace(key), Kind: kind, Value: strings.TrimSpace(value), ValidFrom: validFrom, Source: source, Confidence: confidence, CreatedAt: s.now()})
	if err != nil {
		return Item{}, err
	}
	if s.vectors != nil {
		_ = s.vectors.UpsertVector(ctx, user, m.ID, emb)
	}
	return m, nil
}
func (s *Service) Recall(ctx context.Context, user, query string, limit int) ([]ScoredItem, error) {
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	now := s.now()
	candidates, err := s.repo.CurrentMemories(ctx, user, now, 200)
	if err != nil {
		return nil, err
	}
	qv, err := s.embed.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	semanticScores := map[int64]float64{}
	if s.vectors != nil {
		if hits, e := s.vectors.SearchVectors(ctx, user, qv, limit*8); e == nil {
			for _, h := range hits {
				semanticScores[h.ID] = h.Score
			}
		}
	}
	qt := tokenSet(query)
	out := make([]ScoredItem, 0, len(candidates))
	for _, m := range candidates {
		semantic := semanticScores[m.ID]
		lexical := jaccard(qt, tokenSet(m.Key+" "+m.Value))
		age := now.Sub(m.ValidFrom)
		recency := 1.0 / (1.0 + math.Max(0, age.Hours())/(24*90))
		score := 0.55*semantic + 0.25*lexical + 0.15*recency + 0.05*clamp(m.Confidence, 0, 1)
		if score > 0.05 {
			out = append(out, ScoredItem{Item: m, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *Service) Forget(ctx context.Context, user, key string) error {
	return s.repo.ForgetMemory(ctx, user, strings.TrimSpace(key))
}

// Reindex rebuilds the semantic projection from authoritative current facts.
// The vector store is explicitly secondary: temporal/source-of-truth semantics
// remain in Repository and can always recreate this index.
func (s *Service) Reindex(ctx context.Context, user string) (int, error) {
	if s.vectors == nil {
		return 0, nil
	}
	items, err := s.repo.CurrentMemories(ctx, user, s.now(), 10000)
	if err != nil {
		return 0, err
	}
	indexed := 0
	for _, item := range items {
		v, err := s.embed.Embed(ctx, item.Key+" "+item.Value)
		if err != nil {
			return indexed, err
		}
		if err := s.vectors.UpsertVector(ctx, user, item.ID, v); err != nil {
			return indexed, err
		}
		indexed++
	}
	return indexed, nil
}

func tokenize(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x00C0) })
	out := make([]string, 0, len(f))
	for _, x := range f {
		if len([]rune(x)) > 1 {
			out = append(out, x)
		}
	}
	return out
}
func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, x := range tokenize(s) {
		m[x] = true
	}
	return m
}
func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	i := 0
	u := len(a)
	for k := range b {
		if a[k] {
			i++
		} else {
			u++
		}
	}
	return float64(i) / float64(u)
}
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		dot += float64(a[i] * b[i])
		aa += float64(a[i] * a[i])
		bb += float64(b[i] * b[i])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}
func clamp(v, a, b float64) float64 {
	if v < a {
		return a
	}
	if v > b {
		return b
	}
	return v
}
