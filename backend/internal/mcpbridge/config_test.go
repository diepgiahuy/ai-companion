package mcpbridge

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestValidateEndpointDefaultsToPublicHTTPS(t *testing.T) {
	if _, err := ValidateEndpoint("https://mcp.example.com/api", false); err != nil {
		t.Fatalf("public https endpoint rejected: %v", err)
	}
	for _, endpoint := range []string{
		"http://mcp.example.com/api",
		"https://localhost/mcp",
		"https://127.0.0.1/mcp",
		"https://10.0.0.5/mcp",
		"https://user:pass@mcp.example.com/mcp",
	} {
		if _, err := ValidateEndpoint(endpoint, false); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}

func TestPrivateEndpointRequiresExplicitOptIn(t *testing.T) {
	if _, err := ValidateEndpoint("https://192.168.1.20/mcp", true); err != nil {
		t.Fatalf("explicitly allowed private endpoint rejected: %v", err)
	}
}

func TestServerConfigNormalizesTimeout(t *testing.T) {
	c := ServerConfig{Name: "calendar", Endpoint: "https://mcp.example.com", TimeoutText: "12s"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 12*time.Second {
		t.Fatalf("unexpected timeout %s", c.Timeout)
	}
}

func TestBearerTokenMustExistAtStartup(t *testing.T) {
	c := ServerConfig{Name: "calendar", Endpoint: "https://mcp.example.com", BearerTokenEnv: "MISSING_MCP_TEST_TOKEN"}
	if err := c.Normalize(); err == nil {
		t.Fatal("expected empty bearer token env to fail closed")
	}
}

type captureRoundTripper struct {
	authorization string
}

func (c *captureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	c.authorization = request.Header.Get("Authorization")
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    request,
	}, nil
}

func TestBearerTransportDoesNotLeakToRedirectHost(t *testing.T) {
	t.Setenv("MCP_TEST_TOKEN", "secret")
	base := &captureRoundTripper{}
	transport := bearerTransport{base: base, originHost: "mcp.example.com", tokenEnvVar: "MCP_TEST_TOKEN"}

	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://mcp.example.com/tools", nil)
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if base.authorization != "Bearer secret" {
		t.Fatalf("origin request missing bearer token: %q", base.authorization)
	}

	request, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://other.example.com/tools", nil)
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if base.authorization != "" {
		t.Fatalf("bearer token leaked to another host: %q", base.authorization)
	}
}
