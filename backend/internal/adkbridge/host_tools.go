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

var representativeToolNames = []string{
	ToolExpenseLog,
	ToolBudgetGet,
	ToolTimerCreate,
	ToolMemoryRecall,
}

// RepresentativeToolNames returns a copy so callers cannot mutate the bridge's
// rollout set. CP-SW4 expands this only after the representative parity gate is
// green.
func RepresentativeToolNames() []string {
	return append([]string(nil), representativeToolNames...)
}

type ExpenseLogArgs struct {
	Items []ExpenseItem `json:"items"`
}

type ExpenseItem struct {
	AmountVND   int64  `json:"amount_vnd"`
	Category    string `json:"category"`
	Description string `json:"description"`
	OccurredAt  string `json:"occurred_at"`
}

type BudgetGetArgs struct {
	Period string `json:"period"`
}

type TimerCreateArgs struct {
	Title        string `json:"title,omitempty"`
	DelaySeconds int64  `json:"delay_seconds"`
}

type MemoryRecallArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

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

// ToolExecutionKey is stable for an ADK function call and scoped far enough to
// survive device reconnect/reboot turn-id reuse. FunctionCallID is supplied by
// ADK and is the natural idempotency token for tool retries within an
// invocation.
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
