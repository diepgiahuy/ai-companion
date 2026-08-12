package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
	Pack        string
	// Risk is metadata for the policy layer: read, write, destructive, external.
	Risk        string
	Entitlement string
	FeatureKey  string
}

type ToolRequest struct {
	Key       string
	Arguments string
}
type Presentation struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Primary   string `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
	Progress  int    `json:"progress,omitempty"`
}
type ToolResult struct {
	Content      string
	Presentation *Presentation
}
type Tool interface {
	Name() string
	Definition() *ToolDefinition
	Execute(context.Context, ToolRequest) ToolResult
}

type ToolAuthorizer interface {
	Authorize(context.Context, ToolDefinition, ToolRequest) error
}

type ToolRegistry struct {
	mu         sync.RWMutex
	tools      map[string]Tool
	authorizer ToolAuthorizer
}

func NewToolRegistry() *ToolRegistry                   { return &ToolRegistry{tools: map[string]Tool{}} }
func (r *ToolRegistry) SetAuthorizer(a ToolAuthorizer) { r.mu.Lock(); r.authorizer = a; r.mu.Unlock() }
func (r *ToolRegistry) Register(t Tool) error {
	if t == nil || t.Name() == "" {
		return fmt.Errorf("tool name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name()]; ok {
		return fmt.Errorf("tool %q already registered", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}
func (r *ToolRegistry) Definitions() []ToolDefinition { return r.DefinitionsForPacks(nil) }
func (r *ToolRegistry) DefinitionsForPacks(packs []string) []ToolDefinition {
	allowed := map[string]bool{}
	for _, p := range packs {
		allowed[strings.TrimSpace(p)] = true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ToolDefinition
	for _, t := range r.tools {
		d := t.Definition()
		if d == nil {
			continue
		}
		if len(allowed) > 0 && !allowed[d.Pack] {
			continue
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (r *ToolRegistry) Execute(ctx context.Context, name string, req ToolRequest) ToolResult {
	r.mu.RLock()
	t := r.tools[name]
	a := r.authorizer
	r.mu.RUnlock()
	if t == nil {
		return Failure(fmt.Errorf("unsupported tool %q", name))
	}
	if d := t.Definition(); d != nil {
		if err := ValidateArguments(d.Parameters, req.Arguments); err != nil {
			return Failure(fmt.Errorf("tool arguments rejected: %w", err))
		}
		if a != nil {
			if err := a.Authorize(ctx, *d, req); err != nil {
				return Failure(fmt.Errorf("tool denied: %w", err))
			}
		}
	}
	return t.Execute(ctx, req)
}
func Success(v map[string]any) ToolResult {
	v["ok"] = true
	b, _ := json.Marshal(v)
	return ToolResult{Content: string(b)}
}
func Failure(err error) ToolResult {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
	return ToolResult{Content: string(b)}
}

type FunctionTool struct {
	ToolName       string
	ToolDefinition *ToolDefinition
	Handler        func(context.Context, ToolRequest) ToolResult
}

func (t FunctionTool) Name() string                { return t.ToolName }
func (t FunctionTool) Definition() *ToolDefinition { return t.ToolDefinition }
func (t FunctionTool) Execute(ctx context.Context, r ToolRequest) ToolResult {
	if t.Handler == nil {
		return Failure(fmt.Errorf("tool %q has no handler", t.ToolName))
	}
	return t.Handler(ctx, r)
}

type presentationSinkKey struct{}
type PresentationSink func(Presentation)

func WithPresentationSink(ctx context.Context, sink PresentationSink) context.Context {
	return context.WithValue(ctx, presentationSinkKey{}, sink)
}
func EmitPresentation(ctx context.Context, p Presentation) {
	if sink, ok := ctx.Value(presentationSinkKey{}).(PresentationSink); ok && sink != nil {
		sink(p)
	}
}
