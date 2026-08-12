package capability

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

type Resource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mime_type"`
	Text     string `json:"text"`
}

type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ResourceProvider intentionally resembles MCP resources while remaining an
// in-process Go port. External MCP clients can be adapted behind this interface.
type ResourceProvider interface {
	Schemes() []string
	Read(ctx context.Context, uri *url.URL) (Resource, error)
	List(ctx context.Context) ([]ResourceDescriptor, error)
}

type ResourceRegistry struct {
	mu            sync.RWMutex
	providers     map[string]ResourceProvider
	listProviders []ResourceProvider
}

func NewResourceRegistry() *ResourceRegistry {
	return &ResourceRegistry{providers: make(map[string]ResourceProvider)}
}

func (r *ResourceRegistry) Register(provider ResourceProvider) error {
	if provider == nil {
		return fmt.Errorf("resource provider is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, scheme := range provider.Schemes() {
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme == "" {
			return fmt.Errorf("resource scheme is required")
		}
		if _, exists := r.providers[scheme]; exists {
			return fmt.Errorf("resource scheme %q already registered", scheme)
		}
	}
	for _, scheme := range provider.Schemes() {
		r.providers[strings.ToLower(strings.TrimSpace(scheme))] = provider
	}
	r.listProviders = append(r.listProviders, provider)
	return nil
}

func (r *ResourceRegistry) Read(ctx context.Context, rawURI string) (Resource, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme == "" {
		return Resource{}, fmt.Errorf("invalid resource URI %q", rawURI)
	}
	r.mu.RLock()
	provider := r.providers[strings.ToLower(parsed.Scheme)]
	r.mu.RUnlock()
	if provider == nil {
		return Resource{}, fmt.Errorf("no provider for resource scheme %q", parsed.Scheme)
	}
	return provider.Read(ctx, parsed)
}

func (r *ResourceRegistry) List(ctx context.Context) ([]ResourceDescriptor, error) {
	r.mu.RLock()
	providers := append([]ResourceProvider(nil), r.listProviders...)
	r.mu.RUnlock()
	var result []ResourceDescriptor
	for _, provider := range providers {
		items, err := provider.List(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })
	return result, nil
}
