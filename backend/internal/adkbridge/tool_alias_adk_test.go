//go:build adk

package adkbridge

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type aliasCaptureModel struct {
	seenName string
}

func (m *aliasCaptureModel) Name() string { return "capture" }

func (m *aliasCaptureModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		declaration := req.Config.Tools[0].FunctionDeclarations[0]
		m.seenName = declaration.Name
		yield(&model.LLMResponse{Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
			ID: "call-1", Name: declaration.Name, Args: map[string]any{"content": "hello"},
		}}}}}, nil)
	}
}

func TestProviderToolAliasesRoundTripCanonicalToolName(t *testing.T) {
	inner := &aliasCaptureModel{}
	wrapped := withProviderToolAliases(inner)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: string(genai.RoleUser), Parts: []*genai.Part{{Text: "save note"}}}},
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "note.create", Description: "create note", ParametersJsonSchema: map[string]any{"type": "object"},
		}}}}},
	}
	var got *model.LLMResponse
	for response, err := range wrapped.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		got = response
	}
	if inner.seenName == "note.create" || strings.Contains(inner.seenName, ".") {
		t.Fatalf("provider received unsafe canonical name %q", inner.seenName)
	}
	if got == nil || got.Content == nil || len(got.Content.Parts) != 1 || got.Content.Parts[0].FunctionCall == nil {
		t.Fatalf("response=%+v", got)
	}
	if got.Content.Parts[0].FunctionCall.Name != "note.create" {
		t.Fatalf("round-trip tool name=%q", got.Content.Parts[0].FunctionCall.Name)
	}
	if req.Config.Tools[0].FunctionDeclarations[0].Name != "note.create" {
		t.Fatal("alias wrapper mutated authoritative ToolRegistry-facing request")
	}
}

func TestProviderToolAliasIsStableAndProviderSafe(t *testing.T) {
	first := providerToolAlias("expense.log")
	second := providerToolAlias("expense.log")
	if first != second {
		t.Fatalf("unstable alias: %q != %q", first, second)
	}
	if len(first) > 64 || strings.ContainsAny(first, ".:") {
		t.Fatalf("unsafe alias=%q", first)
	}
}
