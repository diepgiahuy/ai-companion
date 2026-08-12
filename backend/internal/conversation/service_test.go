package conversation

import (
	"context"
	"testing"
	"time"
)

type fakeStore struct {
	reads  int
	clears int
	rows   []Message
}

func (f *fakeStore) Append(context.Context, string, Scope, string, string) error { return nil }
func (f *fakeStore) Recent(context.Context, Scope, int) ([]Message, error) {
	f.reads++
	return append([]Message(nil), f.rows...), nil
}
func (f *fakeStore) Clear(context.Context, Scope) error {
	f.clears++
	f.rows = nil
	return nil
}

func TestServiceUsesWriteThroughCache(t *testing.T) {
	f := &fakeStore{rows: []Message{{Role: "user", Content: "hello"}}}
	s := New(f, NewMemoryCache(time.Minute, 10))
	ctx := context.Background()
	scope := Scope{UserID: "u1", ThreadID: "main"}
	if _, e := s.Recent(ctx, scope, 12); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Recent(ctx, scope, 12); e != nil {
		t.Fatal(e)
	}
	if f.reads != 1 {
		t.Fatalf("expected one durable read, got %d", f.reads)
	}
	if e := s.Append(ctx, "t1", scope, "assistant", "next"); e != nil {
		t.Fatal(e)
	}
	history, e := s.Recent(ctx, scope, 12)
	if e != nil {
		t.Fatal(e)
	}
	if f.reads != 1 {
		t.Fatalf("append should update hot cache, durable reads=%d", f.reads)
	}
	if len(history) != 2 || history[1].Content != "next" {
		t.Fatalf("write-through cache missing append: %#v", history)
	}
}

func TestCacheIsScopedByUserAndThread(t *testing.T) {
	c := NewMemoryCache(time.Minute, 10)
	a := Scope{UserID: "u", ThreadID: "a"}
	b := Scope{UserID: "u", ThreadID: "b"}
	c.Put(a, []Message{{Content: "A"}})
	c.Put(b, []Message{{Content: "B"}})
	x, _ := c.Get(a, 10)
	y, _ := c.Get(b, 10)
	if x[0].Content == y[0].Content {
		t.Fatal("thread caches leaked")
	}
}

func TestClearInvalidatesHotThread(t *testing.T) {
	f := &fakeStore{rows: []Message{{Role: "user", Content: "secret"}}}
	s := New(f, NewMemoryCache(time.Minute, 10))
	scope := Scope{UserID: "u1", ThreadID: "main"}
	if _, err := s.Recent(context.Background(), scope, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(context.Background(), scope); err != nil {
		t.Fatal(err)
	}
	got, err := s.Recent(context.Background(), scope, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || f.clears != 1 || f.reads != 2 {
		t.Fatalf("clear did not invalidate durable/cache state: got=%v clears=%d reads=%d", got, f.clears, f.reads)
	}
}
