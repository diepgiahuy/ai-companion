package adkbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

const (
	ToolExpenseLog   = "expense.log"
	ToolBudgetGet    = "budget.get"
	ToolTimerCreate  = "timer.create"
	ToolMemoryRecall = "memory.recall"
)

// HostToolExecutor is the anti-corruption layer between ADK FunctionTools and
// Companion's authoritative capability registry. The registry remains the one
// place that validates JSON Schema, authorizes access, and executes product
// semantics.
type HostToolExecutor struct {
	Registry *capability.ToolRegistry
}

func (e HostToolExecutor) Execute(ctx context.Context, toolName, functionCallID string, args any) (map[string]any, error) {
	if e.Registry == nil {
		return nil, fmt.Errorf("tool registry is required")
	}
	if strings.TrimSpace(toolName) == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal %s args: %w", toolName, err)
	}
	turn, _ := pipeline.CurrentTurn(ctx)
	definition, exposed := e.Registry.Definition(toolName)
	if !exposed {
		return unexposedToolResult(ctx, functionCallID, toolName), nil
	}
	result := e.Registry.Execute(ctx, toolName, capability.ToolRequest{
		Key:       ToolExecutionKey(turn, functionCallID, toolName),
		Arguments: string(payload),
	})
	var out map[string]any
	if err := json.Unmarshal([]byte(result.Content), &out); err != nil || out == nil {
		return invalidToolResult(ctx, functionCallID, toolName, definition.Risk), nil
	}
	ok, hasOK := out["ok"].(bool)
	if !hasOK {
		return invalidToolResult(ctx, functionCallID, toolName, definition.Risk), nil
	}
	emitToolOutcome(ctx, ToolOutcome{FunctionCallID: functionCallID, Name: toolName, Risk: definition.Risk, OK: ok, Valid: true})
	if ok && result.Presentation != nil {
		capability.EmitPresentation(ctx, *result.Presentation)
	}
	return out, nil
}

func unexposedToolResult(ctx context.Context, functionCallID, toolName string) map[string]any {
	// A provider response may only invoke the definitions explicitly advertised
	// through the capability registry. Internal/legacy tools without a Definition
	// can still be called by trusted in-process code, but never by model output.
	emitToolOutcome(ctx, ToolOutcome{FunctionCallID: functionCallID, Name: toolName, Valid: false})
	return map[string]any{
		"ok":               false,
		"error":            "tool is not exposed to the model",
		"error_code":       "tool_not_exposed",
		"execution_status": "not_started",
		"retryable":        false,
	}
}

func invalidToolResult(ctx context.Context, functionCallID, toolName, risk string) map[string]any {
	// A malformed host result has ambiguous execution status: the side effect
	// may already have committed before serialization failed. Return structured
	// data to the LLM so it can explain the failure, but never expose raw host
	// output and never invite an automatic retry.
	emitToolOutcome(ctx, ToolOutcome{FunctionCallID: functionCallID, Name: toolName, Risk: risk, Valid: false})
	return map[string]any{
		"ok":               false,
		"error":            "internal tool execution failed",
		"error_code":       "invalid_tool_result",
		"execution_status": "unknown",
		"retryable":        false,
	}
}

// ToolExecutionKey is the durable client key supplied by the ADK adapter to the
// domain idempotency contract. It deliberately excludes device/session/turn
// nonces: the same ADK function call retried after reconnect or process restart
// must address the same durable record. Authenticated actor isolation is
// enforced separately by the ledger. Thread keeps reused call IDs from unrelated
// conversations from sharing a key.
func ToolExecutionKey(turn pipeline.TurnContext, functionCallID, toolName string) string {
	return "adk:" + tupleDigest(
		"thread", strings.TrimSpace(turn.ThreadID),
		"function_call", strings.TrimSpace(functionCallID),
		"tool", strings.TrimSpace(toolName),
	)
}

// SessionIdentity maps a Companion turn to ADK's user/session namespace. It is
// deterministic and keeps user sessions isolated even if device-local turn IDs
// restart from zero.
func SessionIdentity(turn pipeline.TurnContext) (userID, sessionID string) {
	user := strings.TrimSpace(turn.UserID)
	device := strings.TrimSpace(turn.DeviceID)
	switch {
	case user != "":
		userID = "user:" + tupleDigest("user", user)
	case device != "":
		userID = "device:" + tupleDigest("device", device)
	default:
		userID = "default"
	}
	thread := strings.TrimSpace(turn.ThreadID)
	nonce := strings.TrimSpace(turn.SessionID)
	sessionID = "session:" + tupleDigest(
		"thread", thread,
		"device", device,
		"session", nonce,
	)
	return userID, sessionID
}

// tupleDigest hashes canonical JSON rather than delimiter-joining identifiers.
// This prevents ambiguous tuples such as ["a:b", "c"] and ["a", "b:c"] from
// colliding while keeping storage keys bounded and safe for logs/databases.
func tupleDigest(parts ...string) string {
	payload, _ := json.Marshal(parts) // []string is always JSON-marshalable.
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
