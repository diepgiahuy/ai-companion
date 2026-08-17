package adkbridge

import (
	"errors"
	"testing"
)

func TestInvocationOutcomeFinalize(t *testing.T) {
	t.Run("plain text completes", func(t *testing.T) {
		var tr invocationOutcomeTracker
		fallback, err := tr.Finalize(true)
		if err != nil || fallback != "" {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("successful mutation after pre-tool text gets deterministic fallback", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordSpeakableText()
		tr.RecordToolCall("call-1")
		tr.RecordTool(ToolOutcome{FunctionCallID: "call-1", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true})
		fallback, err := tr.Finalize(true)
		if err != nil || fallback != mutationFallbackText {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("post-tool text acknowledges successful write", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("call-1")
		tr.RecordTool(ToolOutcome{FunctionCallID: "call-1", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true})
		tr.RecordSpeakableText()
		fallback, err := tr.Finalize(true)
		if err != nil || fallback != "" {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("read result without final text fails", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("call-read")
		tr.RecordTool(ToolOutcome{FunctionCallID: "call-read", Name: ToolBudgetGet, Risk: "read", OK: true, Valid: true})
		_, err := tr.Finalize(false)
		if !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("failed or malformed mutation never gets success fallback", func(t *testing.T) {
		for _, tc := range []ToolOutcome{
			{FunctionCallID: "call-failed", Name: ToolTimerCreate, Risk: "write", OK: false, Valid: true},
			{FunctionCallID: "call-malformed", Name: ToolTimerCreate, Risk: "write", OK: false, Valid: false},
		} {
			var tr invocationOutcomeTracker
			tr.RecordToolCall(tc.FunctionCallID)
			tr.RecordTool(tc)
			fallback, err := tr.Finalize(false)
			if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
				t.Fatalf("outcome=%#v fallback=%q err=%v", tc, fallback, err)
			}
		}
	})

	t.Run("tool call without host result fails even if model spoke before it", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordSpeakableText()
		tr.RecordToolCall("call-missing")
		_, err := tr.Finalize(true)
		if !errors.Is(err, errToolExecutionIncomplete) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("duplicate streamed call and result IDs are idempotent", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("call-1")
		tr.RecordToolCall("call-1")
		outcome := ToolOutcome{FunctionCallID: "call-1", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true}
		tr.RecordTool(outcome)
		tr.RecordTool(outcome)
		fallback, err := tr.Finalize(false)
		if err != nil || fallback != mutationFallbackText {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("later failed tool is never masked by an earlier successful mutation", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("write")
		tr.RecordTool(ToolOutcome{FunctionCallID: "write", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true})
		tr.RecordToolCall("failed-read")
		tr.RecordTool(ToolOutcome{FunctionCallID: "failed-read", Name: ToolBudgetGet, Risk: "read", OK: false, Valid: true})
		fallback, err := tr.Finalize(false)
		if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("successful read followed by write still requires model summary", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("read")
		tr.RecordTool(ToolOutcome{FunctionCallID: "read", Name: ToolBudgetGet, Risk: "read", OK: true, Valid: true})
		tr.RecordToolCall("write")
		tr.RecordTool(ToolOutcome{FunctionCallID: "write", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true})
		fallback, err := tr.Finalize(false)
		if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("later read requires its own final text", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("write")
		tr.RecordTool(ToolOutcome{FunctionCallID: "write", Name: ToolExpenseLog, Risk: "write", OK: true, Valid: true})
		tr.RecordSpeakableText()
		tr.RecordToolCall("read")
		tr.RecordTool(ToolOutcome{FunctionCallID: "read", Name: ToolBudgetGet, Risk: "read", OK: true, Valid: true})
		fallback, err := tr.Finalize(true)
		if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("empty invocation fails", func(t *testing.T) {
		var tr invocationOutcomeTracker
		_, err := tr.Finalize(false)
		if !errors.Is(err, errNoSpeakableText) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("unauthorized mutation fails closed without final text", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("call-unauth")
		tr.RecordTool(ToolOutcome{
			FunctionCallID: "call-unauth",
			Name:           ToolExpenseLog,
			Risk:           "write",
			OK:             false,
			Valid:          true,
		})
		fallback, err := tr.Finalize(false)
		if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("unauthorized mutation must fail closed without final text, got fallback=%q err=%v", fallback, err)
		}
	})

	t.Run("invalid schema mutation fails closed without final text", func(t *testing.T) {
		var tr invocationOutcomeTracker
		tr.RecordToolCall("call-schema-err")
		tr.RecordTool(ToolOutcome{
			FunctionCallID: "call-schema-err",
			Name:           ToolExpenseLog,
			Risk:           "write",
			OK:             false,
			Valid:          false,
		})
		fallback, err := tr.Finalize(false)
		if fallback != "" || !errors.Is(err, errToolFinalTextMissing) {
			t.Fatalf("invalid schema mutation must fail closed without final text, got fallback=%q err=%v", fallback, err)
		}
	})
}

