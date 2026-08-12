package adkbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"companion-server/internal/capability"
	"companion-server/internal/pipeline"
)

type captureTool struct {
	name string
	def  *capability.ToolDefinition
	req  capability.ToolRequest
}

func (t *captureTool) Name() string                           { return t.name }
func (t *captureTool) Definition() *capability.ToolDefinition { return t.def }
func (t *captureTool) Execute(_ context.Context, req capability.ToolRequest) capability.ToolResult {
	t.req = req
	return capability.Success(map[string]any{"tool": t.name})
}

func TestHostToolExecutorDelegatesToAuthoritativeRegistry(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := &captureTool{
		name: ToolBudgetGet,
		def: &capability.ToolDefinition{
			Name: ToolBudgetGet,
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"period": map[string]any{"type": "string"}},
				"required":             []string{"period"},
				"additionalProperties": false,
			},
		},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}

	turn := pipeline.TurnContext{UserID: "u1", ThreadID: "money", DeviceID: "d1", SessionID: "boot-7", TurnID: "42"}
	ctx := pipeline.WithTurnContext(context.Background(), turn)
	out, err := (HostToolExecutor{Registry: reg}).Execute(ctx, ToolBudgetGet, "call-abc", BudgetGetArgs{Period: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["tool"] != ToolBudgetGet {
		t.Fatalf("unexpected output: %#v", out)
	}
	wantKey := ToolExecutionKey(turn, "call-abc", ToolBudgetGet)
	if tool.req.Key != wantKey {
		t.Fatalf("idempotency key=%q, want %q", tool.req.Key, wantKey)
	}
	var args BudgetGetArgs
	if err := json.Unmarshal([]byte(tool.req.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args.Period != "monthly" {
		t.Fatalf("period=%q", args.Period)
	}
}

func TestHostToolExecutorKeepsRegistryValidation(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := &captureTool{
		name: ToolBudgetGet,
		def: &capability.ToolDefinition{
			Name: ToolBudgetGet,
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"period": map[string]any{"type": "string", "enum": []string{"daily", "weekly", "monthly"}}},
				"required":             []string{"period"},
				"additionalProperties": false,
			},
		},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	out, err := (HostToolExecutor{Registry: reg}).Execute(context.Background(), ToolBudgetGet, "call-1", BudgetGetArgs{Period: "yearly"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Fatalf("expected structured registry rejection, got %#v", out)
	}
	if tool.req.Key != "" {
		t.Fatalf("handler should not run after validation failure: %#v", tool.req)
	}
}

func TestSessionIdentityAndToolKeyAreReconnectSafe(t *testing.T) {
	turnA := pipeline.TurnContext{UserID: "u", ThreadID: "t", DeviceID: "d", SessionID: "boot-a", TurnID: "1"}
	turnB := turnA
	turnB.SessionID = "boot-b"
	ua, sa := SessionIdentity(turnA)
	ub, sb := SessionIdentity(turnB)
	if ua != ub || sa == sb {
		t.Fatalf("unexpected identities: %q/%q vs %q/%q", ua, sa, ub, sb)
	}
	if ToolExecutionKey(turnA, "call", ToolTimerCreate) == ToolExecutionKey(turnB, "call", ToolTimerCreate) {
		t.Fatal("idempotency key must differ across server/device session nonce")
	}
}

func TestRepresentativeToolNamesReturnsCopy(t *testing.T) {
	a := RepresentativeToolNames()
	a[0] = "mutated"
	b := RepresentativeToolNames()
	if b[0] == "mutated" {
		t.Fatal("returned slice aliases internal rollout set")
	}
}

type denyAuthorizer struct{}

func (denyAuthorizer) Authorize(context.Context, capability.ToolDefinition, capability.ToolRequest) error {
	return errors.New("denied by test policy")
}

func TestHostToolExecutorKeepsRegistryAuthorization(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := &captureTool{
		name: ToolBudgetGet,
		def: &capability.ToolDefinition{
			Name:       ToolBudgetGet,
			Parameters: map[string]any{"type": "object", "additionalProperties": true},
		},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	reg.SetAuthorizer(denyAuthorizer{})
	out, err := (HostToolExecutor{Registry: reg}).Execute(context.Background(), ToolBudgetGet, "call-auth", BudgetGetArgs{Period: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Fatalf("expected structured authorization rejection, got %#v", out)
	}
	if tool.req.Key != "" {
		t.Fatalf("handler should not run after authorization failure: %#v", tool.req)
	}
}

func TestToolExecutionKeyCanonicalTupleHasNoDelimiterCollision(t *testing.T) {
	a := pipeline.TurnContext{UserID: "a:b", ThreadID: "c", DeviceID: "d", SessionID: "s", TurnID: "t"}
	b := pipeline.TurnContext{UserID: "a", ThreadID: "b:c", DeviceID: "d", SessionID: "s", TurnID: "t"}
	ka := ToolExecutionKey(a, "call", ToolBudgetGet)
	kb := ToolExecutionKey(b, "call", ToolBudgetGet)
	if ka == kb {
		t.Fatalf("canonical tuple keys collided: %q", ka)
	}
	if ka != ToolExecutionKey(a, "call", ToolBudgetGet) {
		t.Fatal("idempotency key must be deterministic")
	}
}

type malformedResultTool struct {
	name         string
	def          *capability.ToolDefinition
	content      string
	presentation *capability.Presentation
}

func (t malformedResultTool) Name() string                           { return t.name }
func (t malformedResultTool) Definition() *capability.ToolDefinition { return t.def }
func (t malformedResultTool) Execute(context.Context, capability.ToolRequest) capability.ToolResult {
	return capability.ToolResult{Content: t.content, Presentation: t.presentation}
}

func TestHostToolExecutorConvertsMalformedHostResultToSafeStructuredFailure(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := malformedResultTool{
		name: ToolTimerCreate,
		def: &capability.ToolDefinition{
			Name:       ToolTimerCreate,
			Risk:       "write",
			Parameters: map[string]any{"type": "object", "additionalProperties": true},
		},
		content:      `backend panic detail: secret-token-123`,
		presentation: &capability.Presentation{Kind: "success", Title: "must not publish"},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}

	var gotOutcome ToolOutcome
	presentations := 0
	ctx := capability.WithPresentationSink(context.Background(), func(capability.Presentation) { presentations++ })
	ctx = withToolOutcomeSink(ctx, func(outcome ToolOutcome) { gotOutcome = outcome })
	out, err := (HostToolExecutor{Registry: reg}).Execute(ctx, ToolTimerCreate, "call-malformed", TimerCreateArgs{DelaySeconds: 60})
	if err != nil {
		t.Fatalf("malformed host output must become a tool response, got error: %v", err)
	}
	if out["ok"] != false || out["error_code"] != "invalid_tool_result" || out["execution_status"] != "unknown" || out["retryable"] != false {
		t.Fatalf("unexpected safe failure payload: %#v", out)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || strings.Contains(string(encoded), "secret-token-123") {
		t.Fatalf("raw host output leaked to model: %s", encoded)
	}
	if gotOutcome.Name != ToolTimerCreate || gotOutcome.Risk != "write" || gotOutcome.Valid || gotOutcome.OK {
		t.Fatalf("unexpected outcome: %#v", gotOutcome)
	}
	if presentations != 0 {
		t.Fatalf("malformed tool result published %d presentation(s)", presentations)
	}
}

func TestHostToolExecutorRejectsJSONWithoutBooleanOKEnvelope(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := malformedResultTool{
		name: ToolBudgetGet,
		def: &capability.ToolDefinition{
			Name:       ToolBudgetGet,
			Risk:       "read",
			Parameters: map[string]any{"type": "object", "additionalProperties": true},
		},
		content: `{"value":123}`,
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	out, err := (HostToolExecutor{Registry: reg}).Execute(context.Background(), ToolBudgetGet, "call-no-envelope", BudgetGetArgs{Period: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	if out["error_code"] != "invalid_tool_result" || out["execution_status"] != "unknown" {
		t.Fatalf("unexpected payload: %#v", out)
	}
}

func TestHostToolExecutorDoesNotPublishPresentationForValidFailure(t *testing.T) {
	reg := capability.NewToolRegistry()
	tool := malformedResultTool{
		name: ToolTimerCreate,
		def: &capability.ToolDefinition{
			Name:       ToolTimerCreate,
			Risk:       "write",
			Parameters: map[string]any{"type": "object", "additionalProperties": true},
		},
		content:      `{"ok":false,"error":"rejected"}`,
		presentation: &capability.Presentation{Kind: "success", Title: "must not publish"},
	}
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	presentations := 0
	ctx := capability.WithPresentationSink(context.Background(), func(capability.Presentation) { presentations++ })
	out, err := (HostToolExecutor{Registry: reg}).Execute(ctx, ToolTimerCreate, "call-failed", TimerCreateArgs{DelaySeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Fatalf("unexpected tool result: %#v", out)
	}
	if presentations != 0 {
		t.Fatalf("failed tool result published %d presentation(s)", presentations)
	}
}
