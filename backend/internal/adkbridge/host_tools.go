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
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal %s args: %w", toolName, err)
	}
	turn, _ := pipeline.CurrentTurn(ctx)
	definition, _ := e.Registry.Definition(toolName)
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

// ToolExecutionKey identifies one ADK tool execution inside a Companion turn.
// Durable mutation idempotency remains owned by the domain/store contract (#27),
// not by the model's function-call identifier alone.
func ToolExecutionKey(turn pipeline.TurnContext, functionCallID, toolName string) string {
	return "adk:" + tupleDigest(
		"user", strings.TrimSpace(turn.UserID),
		"thread", strings.TrimSpace(turn.ThreadID),
		"device", strings.TrimSpace(turn.DeviceID),
		"session", strings.TrimSpace(turn.SessionID),
		"turn", strings.TrimSpace(turn.TurnID),
		"function_call", strings.TrimSpace(functionCallID),
		"tool", strings.TrimSpace(toolName),
	)
}

// SessionIdentity maps a Companion owner/thread to ADK's user/session namespace.
// Transport SessionID is intentionally excluded: reconnect creates a new socket
// session but must not discard durable conversational continuity.
func SessionIdentity(turn pipeline.TurnContext) (userID, sessionID string) {
	user := strings.TrimSpace(turn.UserID)
	device := strings.TrimSpace(turn.DeviceID)
	thread := strings.TrimSpace(turn.ThreadID)
	if user != "" {
		userID = "user:" + tupleDigest("user", user)
	} else if device != "" {
		userID = "device:" + tupleDigest("device", device)
	} else {
		userID = "default"
	}
	if thread != "" {
		sessionID = "thread:" + tupleDigest("user", user, "thread", thread)
	} else if device != "" {
		sessionID = "device-thread:" + tupleDigest("user", user, "device", device)
	} else {
		sessionID = "default"
	}
	return userID, sessionID
}

// tupleDigest hashes canonical JSON rather than delimiter-joining identifiers.
func tupleDigest(parts ...string) string {
	payload, _ := json.Marshal(parts) // []string is always JSON-marshalable.
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
