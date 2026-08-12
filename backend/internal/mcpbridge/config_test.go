package mcpbridge

import (
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
	c := ServerConfig{TimeoutText: "12s"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 12*time.Second {
		t.Fatalf("unexpected timeout %s", c.Timeout)
	}
}
