package memory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"companion-server/internal/idempotency"
)

type memItem struct {
	Item
	DeletedAt *time.Time
}

type memRepo struct {
	mu      sync.Mutex
	xs      []memItem
	vectors map[string]map[int64][]float32
	ledger  map[string]any
}

func newMemRepo() *memRepo {
	return &memRepo{
		vectors: make(map[string]map[int64][]float32),
		ledger:  make(map[string]any),
	}
}

func (r *memRepo) UpsertMemory(_ context.Context, x Item) (Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.xs {
		if r.xs[i].UserID == x.UserID && r.xs[i].Key == x.Key && r.xs[i].ValidTo == nil && r.xs[i].DeletedAt == nil {
			z := x.ValidFrom
			r.xs[i].ValidTo = &z
		}
	}
	x.ID = int64(len(r.xs) + 1)
	r.xs = append(r.xs, memItem{Item: x})
	return x, nil
}

func (r *memRepo) UpsertMemoryMutation(_ context.Context, req idempotency.Request, x Item) (Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := req.Actor + ":" + req.Operation + ":" + req.Key
	if prev, exists := r.ledger[key]; exists {
		return prev.(Item), nil
	}
	for i := range r.xs {
		if r.xs[i].UserID == x.UserID && r.xs[i].Key == x.Key && r.xs[i].ValidTo == nil && r.xs[i].DeletedAt == nil {
			z := x.ValidFrom
			r.xs[i].ValidTo = &z
		}
	}
	x.ID = int64(len(r.xs) + 1)
	r.xs = append(r.xs, memItem{Item: x})
	r.ledger[key] = x
	return x, nil
}

func (r *memRepo) CurrentMemories(_ context.Context, u string, n time.Time, l int) ([]Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var o []Item
	for _, x := range r.xs {
		if x.UserID == u && x.DeletedAt == nil && !x.ValidFrom.After(n) && (x.ValidTo == nil || x.ValidTo.After(n)) {
			o = append(o, x.Item)
		}
	}
	if l > 0 && len(o) > l {
		o = o[:l]
	}
	return o, nil
}

func (r *memRepo) ForgetMemory(_ context.Context, u, k string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := time.Now()
	for i := range r.xs {
		if r.xs[i].UserID == u && r.xs[i].Key == k && r.xs[i].DeletedAt == nil {
			r.xs[i].DeletedAt = &n
			if r.xs[i].ValidTo == nil {
				r.xs[i].ValidTo = &n
			}
		}
	}
	return nil
}

func (r *memRepo) ForgetMemoryMutation(_ context.Context, req idempotency.Request, u, k string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := req.Actor + ":" + req.Operation + ":" + req.Key
	if _, exists := r.ledger[key]; exists {
		return nil
	}
	n := time.Now()
	for i := range r.xs {
		if r.xs[i].UserID == u && r.xs[i].Key == k && r.xs[i].DeletedAt == nil {
			r.xs[i].DeletedAt = &n
			if r.xs[i].ValidTo == nil {
				r.xs[i].ValidTo = &n
			}
		}
	}
	r.ledger[key] = true
	return nil
}

func (r *memRepo) UpsertVector(_ context.Context, user string, memoryID int64, vec []float32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vectors[user] == nil {
		r.vectors[user] = make(map[int64][]float32)
	}
	r.vectors[user][memoryID] = vec
	return nil
}

func (r *memRepo) DeleteVector(_ context.Context, user string, memoryID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.vectors[user] != nil {
		delete(r.vectors[user], memoryID)
	}
	return nil
}

func (r *memRepo) SearchVectors(_ context.Context, user string, query []float32, limit int) ([]VectorHit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	userVecs := r.vectors[user]
	var hits []VectorHit
	for id, vec := range userVecs {
		score := cosine(query, vec)
		hits = append(hits, VectorHit{ID: id, Score: score})
	}
	return hits, nil
}

var _ Repository = (*memRepo)(nil)
var _ DurableRepository = (*memRepo)(nil)
var _ VectorStore = (*memRepo)(nil)

func TestTemporalSupersedeAndHybridRecall(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	_, _ = s.Remember(context.Background(), "u", "lunch_budget", Temporal, "70000 VND", "user", 1, now.Add(-24*time.Hour))
	_, _ = s.Remember(context.Background(), "u", "lunch_budget", Temporal, "90000 VND", "user", 1, now)
	xs, e := s.Recall(context.Background(), "u", "budget lunch", 5)
	if e != nil || len(xs) != 1 || xs[0].Item.Value != "90000 VND" {
		t.Fatalf("%+v %v", xs, e)
	}
}

func TestOwnerIsolationInRecallAndQueries(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	// User A and User B both remember distinct values for the same key
	_, err := s.Remember(ctx, "user_alpha", "coffee_preference", Semantic, "iced black coffee with no sugar", "user", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Remember(ctx, "user_beta", "coffee_preference", Semantic, "hot latte with almond milk", "user", 1, now)
	if err != nil {
		t.Fatal(err)
	}

	// User A recalls
	alphaHits, err := s.Recall(ctx, "user_alpha", "coffee preference", 5)
	if err != nil || len(alphaHits) != 1 || alphaHits[0].Item.Value != "iced black coffee with no sugar" {
		t.Fatalf("alpha recall: got %+v err=%v", alphaHits, err)
	}

	// User B recalls
	betaHits, err := s.Recall(ctx, "user_beta", "coffee preference", 5)
	if err != nil || len(betaHits) != 1 || betaHits[0].Item.Value != "hot latte with almond milk" {
		t.Fatalf("beta recall: got %+v err=%v", betaHits, err)
	}

	// User C has no memories
	gammaHits, err := s.Recall(ctx, "user_gamma", "coffee preference", 5)
	if err != nil || len(gammaHits) != 0 {
		t.Fatalf("gamma recall: got %+v err=%v", gammaHits, err)
	}

	// User A forgets their memory
	if err := s.Forget(ctx, "user_alpha", "coffee_preference"); err != nil {
		t.Fatal(err)
	}

	// User A no longer sees it, User B is completely untouched
	alphaAfter, err := s.Recall(ctx, "user_alpha", "coffee preference", 5)
	if err != nil || len(alphaAfter) != 0 {
		t.Fatalf("alpha after forget: got %+v", alphaAfter)
	}
	betaAfter, err := s.Recall(ctx, "user_beta", "coffee preference", 5)
	if err != nil || len(betaAfter) != 1 || betaAfter[0].Item.Value != "hot latte with almond milk" {
		t.Fatalf("beta after alpha forget: got %+v", betaAfter)
	}
}

func TestPointInTimeTemporalResolution(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	t0 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Remember v1 at t0
	s.now = func() time.Time { return t0 }
	_, err := s.Remember(ctx, "u", "home_city", Temporal, "Da Nang", "user", 1, t0)
	if err != nil {
		t.Fatal(err)
	}

	// Remember v2 at t1 (relocated to Ho Chi Minh City)
	s.now = func() time.Time { return t1 }
	_, err = s.Remember(ctx, "u", "home_city", Temporal, "Ho Chi Minh City", "user", 1, t1)
	if err != nil {
		t.Fatal(err)
	}

	// Query at T before T0: should be empty
	beforeT0, err := r.CurrentMemories(ctx, "u", t0.Add(-time.Hour), 10)
	if err != nil || len(beforeT0) != 0 {
		t.Fatalf("before T0: got %+v", beforeT0)
	}

	// Query at T0: should return Da Nang
	atT0, err := r.CurrentMemories(ctx, "u", t0.Add(time.Hour), 10)
	if err != nil || len(atT0) != 1 || atT0[0].Value != "Da Nang" {
		t.Fatalf("at T0: got %+v", atT0)
	}

	// Query at T1: should return Ho Chi Minh City
	atT1, err := r.CurrentMemories(ctx, "u", t1.Add(time.Hour), 10)
	if err != nil || len(atT1) != 1 || atT1[0].Value != "Ho Chi Minh City" {
		t.Fatalf("at T1: got %+v", atT1)
	}
}

func TestRememberAndForgetMutationIdempotency(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	req := idempotency.Request{
		Actor:       "actor-1",
		Operation:   "memory.remember",
		Key:         "idem-mem-key-1",
		RequestHash: strings.Repeat("a", 64),
	}

	m1, err := s.RememberMutation(ctx, req, "u1", "favorite_color", Semantic, "blue", "user", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.RememberMutation(ctx, req, "u1", "favorite_color", Semantic, "blue", "user", 1, now)
	if err != nil {
		t.Fatal(err)
	}
	if m1.ID != m2.ID || m1.Value != m2.Value {
		t.Fatalf("mutation replay mismatch: m1=%+v m2=%+v", m1, m2)
	}

	// Forget mutation replay
	forgetReq := idempotency.Request{
		Actor:       "actor-1",
		Operation:   "memory.forget",
		Key:         "idem-forget-key-1",
		RequestHash: strings.Repeat("b", 64),
	}
	if err := s.ForgetMutation(ctx, forgetReq, "u1", "favorite_color"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetMutation(ctx, forgetReq, "u1", "favorite_color"); err != nil {
		t.Fatal(err)
	}

	current, err := r.CurrentMemories(ctx, "u1", now, 10)
	if err != nil || len(current) != 0 {
		t.Fatalf("current after idempotent forget: got %+v", current)
	}
}

func TestReindexAuthoritativeProjection(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	// Insert memories directly into repository (simulating DB migration / restore)
	item1, _ := r.UpsertMemory(ctx, Item{UserID: "u_reindex", Key: "skill_1", Kind: Semantic, Value: "Go programming", ValidFrom: now, Source: "test", Confidence: 1, CreatedAt: now})
	item2, _ := r.UpsertMemory(ctx, Item{UserID: "u_reindex", Key: "skill_2", Kind: Semantic, Value: "PostgreSQL DBA", ValidFrom: now, Source: "test", Confidence: 1, CreatedAt: now})

	// Before reindex, vector store is empty
	if len(r.vectors["u_reindex"]) != 0 {
		t.Fatalf("expected empty vectors, got %d", len(r.vectors["u_reindex"]))
	}

	count, err := s.Reindex(ctx, "u_reindex")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("reindexed count=%d want 2", count)
	}
	if len(r.vectors["u_reindex"]) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(r.vectors["u_reindex"]))
	}
	if _, ok := r.vectors["u_reindex"][item1.ID]; !ok {
		t.Fatalf("missing vector for item 1")
	}
	if _, ok := r.vectors["u_reindex"][item2.ID]; !ok {
		t.Fatalf("missing vector for item 2")
	}
}

func TestVietnameseAndLexicalTokenization(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{
			input: "bữa trưa ăn phở bò",
			want:  []string{"bữa", "trưa", "ăn", "phở", "bò"},
		},
		{
			input: "lunch_budget_vnd_50000",
			want:  []string{"lunch", "budget", "vnd", "50000"},
		},
		{
			input: "ngân sách chi tiêu hàng ngày: 200k",
			want:  []string{"ngân", "sách", "chi", "tiêu", "hàng", "ngày", "200k"},
		},
	}

	for _, tc := range tests {
		got := tokenize(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("tokenize(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestUnrelatedQueryRelevanceGating(t *testing.T) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	_, err := s.Remember(ctx, "user_gate", "diet_note", Semantic, "allergic to peanuts and shellfish", "user", 1, now)
	if err != nil {
		t.Fatal(err)
	}

	// Completely unrelated query should NOT match either lexically or semantically above threshold
	// and should yield zero hits rather than injecting peanut allergy context into flight bookings.
	hits, err := s.Recall(ctx, "user_gate", "flight booking airplane schedule", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Item.Key == "diet_note" && h.Score <= 0.05 {
			t.Fatalf("unrelated recall leaked item with low score: %+v", h)
		}
	}
}

func BenchmarkDeterministicRecall(b *testing.B) {
	r := newMemRepo()
	s := New(r, HashEmbedding{Dimensions: 64})
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("pref_%d", i)
		val := fmt.Sprintf("preference value for category %d with some descriptive text", i%10)
		_, _ = s.Remember(ctx, "bench_user", key, Semantic, val, "user", 1, now.Add(time.Duration(-i)*time.Hour))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := s.Recall(ctx, "bench_user", "category 5 preference", 5)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHashEmbedding(b *testing.B) {
	emb := HashEmbedding{Dimensions: 96}
	ctx := context.Background()
	sampleText := "ngân sách chi tiêu hàng ngày cho bữa trưa và cà phê là 150000 VND"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := emb.Embed(ctx, sampleText)
		if err != nil {
			b.Fatal(err)
		}
	}
}
