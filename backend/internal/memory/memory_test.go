package memory

import (
	"context"
	"testing"
	"time"
)

type memRepo struct{ xs []Item }

func (r *memRepo) UpsertMemory(_ context.Context, x Item) (Item, error) {
	for i := range r.xs {
		if r.xs[i].Key == x.Key && r.xs[i].ValidTo == nil {
			z := x.ValidFrom
			r.xs[i].ValidTo = &z
		}
	}
	x.ID = int64(len(r.xs) + 1)
	r.xs = append(r.xs, x)
	return x, nil
}
func (r *memRepo) CurrentMemories(_ context.Context, u string, n time.Time, l int) ([]Item, error) {
	var o []Item
	for _, x := range r.xs {
		if x.UserID == u && !x.ValidFrom.After(n) && (x.ValidTo == nil || x.ValidTo.After(n)) {
			o = append(o, x)
		}
	}
	return o, nil
}
func (r *memRepo) ForgetMemory(_ context.Context, u, k string) error {
	n := time.Now()
	for i := range r.xs {
		if r.xs[i].UserID == u && r.xs[i].Key == k && r.xs[i].ValidTo == nil {
			r.xs[i].ValidTo = &n
		}
	}
	return nil
}
func TestTemporalSupersedeAndHybridRecall(t *testing.T) {
	r := &memRepo{}
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
