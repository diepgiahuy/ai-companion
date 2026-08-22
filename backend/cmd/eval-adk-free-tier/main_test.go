//go:build adk

package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"companion-server/internal/adkbridge"
	"companion-server/internal/capability"
)

func TestProbeRegistryExecutesCanonicalReadOnlyTool(t *testing.T) {
	var executions atomic.Int64
	var lastQuery atomic.Value
	registry, err := buildProbeRegistry(&executions, &lastQuery)
	if err != nil {
		t.Fatal(err)
	}
	result := registry.Execute(context.Background(), probeToolName, capability.ToolRequest{
		Key:       "probe-1",
		Arguments: `{"query":"adk-production-path"}`,
	})
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(result.Content), &body); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !body.OK {
		t.Fatalf("tool result=%s", result.Content)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("executions=%d want=1", got)
	}
	if got, _ := lastQuery.Load().(string); got != probeQuery {
		t.Fatalf("query=%q want=%q", got, probeQuery)
	}
}

func TestRunFailsClosedWithoutCredential(t *testing.T) {
	report, err := run(context.Background(), "", "deadbeef", "unit-test")
	if err == nil {
		t.Fatal("missing credential unexpectedly accepted")
	}
	if report.Status != "FAIL" {
		t.Fatalf("status=%q", report.Status)
	}
	if report.Model != "gemma-4-31b-it" {
		t.Fatalf("model=%q", report.Model)
	}
	if report.Protocol != adkbridge.ModelProtocolChatCompletions {
		t.Fatalf("protocol=%q", report.Protocol)
	}
	if report.ProviderBase != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("provider_base=%q", report.ProviderBase)
	}
	if !report.ProviderToolAliases {
		t.Fatal("provider tool aliases must be enabled")
	}
	if report.Retries != 0 || report.RequestSpacingMS != 15000 {
		t.Fatalf("retries=%d spacing_ms=%d", report.Retries, report.RequestSpacingMS)
	}
	if report.ToolExecutions != 0 {
		t.Fatalf("tool executions=%d", report.ToolExecutions)
	}
}
