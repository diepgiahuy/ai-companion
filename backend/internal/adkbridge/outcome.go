package adkbridge

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ToolOutcome is Companion-owned execution metadata emitted by the host-tool
// bridge. It deliberately contains no provider/framework types so runtime
// correctness rules can be tested without the ADK dependency graph.
type ToolOutcome struct {
	FunctionCallID string
	Name           string
	Risk           string
	OK             bool
	Valid          bool
}

var (
	errToolExecutionIncomplete = errors.New("ADK tool call did not produce a host result")
	errToolFinalTextMissing    = errors.New("ADK ended after tool execution without final speakable response")
	errNoSpeakableText         = errors.New("ADK returned no speakable text")
)

const mutationFallbackText = "OK."

type toolOutcomeSinkKey struct{}
type toolOutcomeSink func(ToolOutcome)

func withToolOutcomeSink(ctx context.Context, sink toolOutcomeSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, toolOutcomeSinkKey{}, sink)
}

func emitToolOutcome(ctx context.Context, outcome ToolOutcome) {
	if sink, ok := ctx.Value(toolOutcomeSinkKey{}).(toolOutcomeSink); ok && sink != nil {
		sink(outcome)
	}
}

// invocationOutcomeTracker distinguishes text emitted before a tool from text
// emitted after the tool result. It also correlates ADK function-call IDs so a
// partial/duplicate event cannot make an incomplete tool execution look done.
type invocationOutcomeTracker struct {
	mu sync.Mutex

	expectedCallIDs  map[string]struct{}
	completedCallIDs map[string]struct{}
	anonymousCalls   uint64
	anonymousResults uint64

	toolResults               uint64
	lastSuccessfulMutation    uint64
	lastNonFallbackableResult uint64
	lastTextAfterToolResult   uint64
}

func (t *invocationOutcomeTracker) RecordToolCall(functionCallID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := strings.TrimSpace(functionCallID)
	if id == "" {
		t.anonymousCalls++
		return
	}
	if t.expectedCallIDs == nil {
		t.expectedCallIDs = make(map[string]struct{})
	}
	t.expectedCallIDs[id] = struct{}{}
}

func (t *invocationOutcomeTracker) RecordTool(outcome ToolOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := strings.TrimSpace(outcome.FunctionCallID)
	if id != "" {
		if t.completedCallIDs == nil {
			t.completedCallIDs = make(map[string]struct{})
		}
		if _, duplicate := t.completedCallIDs[id]; duplicate {
			return
		}
		t.completedCallIDs[id] = struct{}{}
	} else {
		t.anonymousResults++
	}

	t.toolResults++
	if outcome.Valid && outcome.OK && isMutationRisk(outcome.Risk) {
		t.lastSuccessfulMutation = t.toolResults
	} else {
		// A read result, explicit tool failure, or malformed/ambiguous result
		// must be summarized by the model. Never let a nearby successful
		// mutation's fallback acknowledgement mask it.
		t.lastNonFallbackableResult = t.toolResults
	}
}

func (t *invocationOutcomeTracker) RecordSpeakableText() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.toolResults > 0 {
		t.lastTextAfterToolResult = t.toolResults
	}
}

func (t *invocationOutcomeTracker) hasIncompleteToolCallLocked() bool {
	for id := range t.expectedCallIDs {
		if _, done := t.completedCallIDs[id]; !done {
			return true
		}
	}
	return t.anonymousResults < t.anonymousCalls
}

// Finalize validates the terminal state of one ADK invocation. A deterministic
// acknowledgement is allowed only for a successfully committed mutation that
// has not received post-tool model text. Every other tool-without-final-text
// state remains an error so framework/provider regressions cannot be hidden.
func (t *invocationOutcomeTracker) Finalize(sentText bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.hasIncompleteToolCallLocked() {
		return "", errToolExecutionIncomplete
	}
	if t.toolResults > t.lastTextAfterToolResult {
		if t.lastNonFallbackableResult > t.lastTextAfterToolResult {
			return "", errToolFinalTextMissing
		}
		if t.lastSuccessfulMutation > t.lastTextAfterToolResult {
			return mutationFallbackText, nil
		}
		return "", errToolFinalTextMissing
	}
	if !sentText {
		return "", errNoSpeakableText
	}
	return "", nil
}

func isMutationRisk(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "write", "destructive":
		return true
	default:
		return false
	}
}
