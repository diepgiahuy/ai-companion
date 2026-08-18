package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"companion-server/internal/observability"
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

// ContextAvailability lets a registered tool fail closed when the trusted
// invocation context does not currently support it. Model exposure may use the
// same predicate, but Execute always rechecks it so visibility is never treated
// as authorization.
type ContextAvailability interface {
	Available(context.Context) bool
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
	if t == nil {
		return fmt.Errorf("tool name is required")
	}
	name := strings.TrimSpace(t.Name())
	if name == "" || name != t.Name() {
		return fmt.Errorf("tool name must be non-empty and canonical")
	}
	if definition := t.Definition(); definition != nil {
		if strings.TrimSpace(definition.Name) == "" || definition.Name != name {
			return fmt.Errorf("tool %q definition name must match registry name", name)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("tool %q already registered", name)
	}
	r.tools[name] = t
	return nil
}
func (r *ToolRegistry) Definitions() []ToolDefinition { return r.DefinitionsForPacks(nil) }

// Definition returns one registered tool definition without exposing the tool
// implementation. Adapters use this to reuse the capability registry as the
// schema source of truth instead of maintaining a second schema catalog.
func (r *ToolRegistry) Definition(name string) (ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t := r.tools[name]
	if t == nil || t.Definition() == nil {
		return ToolDefinition{}, false
	}
	return *t.Definition(), true
}

func toolAvailableInContext(ctx context.Context, t Tool) bool {
	if t == nil {
		return false
	}
	if guard, ok := t.(ContextAvailability); ok {
		return guard.Available(ctx)
	}
	// Device-pack tools represent hardware/session-dependent behavior. Missing
	// a context guard is a contract bug, so fail closed instead of exposing the
	// definition process-wide.
	if definition := t.Definition(); definition != nil && strings.TrimSpace(definition.Pack) == "device" {
		return false
	}
	return true
}

// Available reports whether a model-visible registered tool is currently
// usable in this trusted context. Tools without a definition are not eligible
// for model exposure. Device-pack tools require an explicit context guard.
func (r *ToolRegistry) Available(ctx context.Context, name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	t := r.tools[name]
	r.mu.RUnlock()
	if t == nil || t.Definition() == nil {
		return false
	}
	return toolAvailableInContext(ctx, t)
}

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
func (r *ToolRegistry) Execute(ctx context.Context, name string, req ToolRequest) (result ToolResult) {
	started := time.Now()
	recordedName := "unsupported"
	risk := ""
	defer func() {
		if recover() != nil {
			result = Failure(fmt.Errorf("internal tool execution failed"))
		}
		outcome := "error"
		var marker struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal([]byte(result.Content), &marker) == nil && marker.OK {
			outcome = "ok"
		}
		observability.Record(ctx, observability.Event{
			Name: observability.EventToolEnd, DurationMS: time.Since(started).Milliseconds(),
			Outcome: outcome, ToolName: recordedName, ToolRisk: risk,
		})
	}()

	r.mu.RLock()
	t := r.tools[name]
	a := r.authorizer
	r.mu.RUnlock()
	if t == nil {
		return Failure(fmt.Errorf("unsupported tool %q", name))
	}
	recordedName = t.Name()
	if !toolAvailableInContext(ctx, t) {
		return Failure(fmt.Errorf("tool %q unavailable in current context", name))
	}
	if d := t.Definition(); d != nil {
		risk = d.Risk
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
	out := make(map[string]any)
	for key, value := range v {
		out[key] = value
	}
	out["ok"] = true
	b, _ := json.Marshal(out)
	return ToolResult{Content: string(b)}
}
func Failure(err error) ToolResult {
	if err == nil {
		err = fmt.Errorf("unknown tool failure")
	}
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
