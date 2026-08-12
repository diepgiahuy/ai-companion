package agent

import "context"

type ModelSelector interface {
	Select(context.Context, string) string
}

// StaticModelSelector is the deterministic no-classifier policy. It is the
// safe fallback when semantic routing is not configured; unlike the old
// KeywordModelSelector it does not inspect user text with brittle substrings.
type StaticModelSelector struct {
	Model string
}

func (s StaticModelSelector) Select(_ context.Context, _ string) string {
	return s.Model
}
