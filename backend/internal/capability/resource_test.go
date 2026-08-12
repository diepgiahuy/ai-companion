package capability

import (
	"context"
	"net/url"
	"testing"
)

type fakeResourceProvider struct{}

func (fakeResourceProvider) Schemes() []string { return []string{"demo"} }
func (fakeResourceProvider) Read(_ context.Context, uri *url.URL) (Resource, error) {
	return Resource{URI: uri.String(), MIMEType: "text/plain", Text: "ok"}, nil
}
func (fakeResourceProvider) List(context.Context) ([]ResourceDescriptor, error) {
	return []ResourceDescriptor{{URI: "demo://current", Name: "demo"}}, nil
}

func TestResourceRegistryRoutesByScheme(t *testing.T) {
	registry := NewResourceRegistry()
	if err := registry.Register(fakeResourceProvider{}); err != nil {
		t.Fatal(err)
	}
	resource, err := registry.Read(context.Background(), "demo://current")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Text != "ok" {
		t.Fatalf("resource = %+v", resource)
	}
	items, err := registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URI != "demo://current" {
		t.Fatalf("items = %+v", items)
	}
}
