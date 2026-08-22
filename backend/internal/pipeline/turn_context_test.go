package pipeline

import (
	"context"
	"testing"
)

func TestWithTurnContextCapturesParentLifetime(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx := WithTurnContext(parent, TurnContext{SessionID: "session", TurnID: "turn"})
	turn, ok := CurrentTurn(ctx)
	if !ok || turn.Done == nil {
		t.Fatal("turn lifetime is missing")
	}

	child, cancelChild := context.WithCancel(ctx)
	cancelChild()
	select {
	case <-turn.Done:
		t.Fatal("child cancellation must not cancel the canonical turn")
	default:
	}

	cancelParent()
	select {
	case <-turn.Done:
	default:
		t.Fatal("parent cancellation must cancel the canonical turn")
	}
	_ = child
}

func TestWithTurnContextPreservesExplicitLifetime(t *testing.T) {
	explicit, cancelExplicit := context.WithCancel(context.Background())
	defer cancelExplicit()
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	ctx := WithTurnContext(parent, TurnContext{
		SessionID: "session",
		TurnID:    "turn",
		Done:      explicit.Done(),
	})
	turn, ok := CurrentTurn(ctx)
	if !ok {
		t.Fatal("turn context is missing")
	}

	cancelParent()
	select {
	case <-turn.Done:
		t.Fatal("explicit turn lifetime changed")
	default:
	}
	cancelExplicit()
	select {
	case <-turn.Done:
	default:
		t.Fatal("explicit turn lifetime did not cancel")
	}
}
