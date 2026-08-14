package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"companion-server/internal/capability"
)

var ErrNotBuilt = errors.New("MCP integration is not included in this build; rebuild with -tags=mcp")

type ServerConfig struct {
	Name                string        `json:"name"`
	Endpoint            string        `json:"endpoint"`
	Pack                string        `json:"pack,omitempty"`
	Entitlement         string        `json:"entitlement,omitempty"`
	FeatureKey          string        `json:"feature_key,omitempty"`
	AllowPrivateNetwork bool          `json:"allow_private_network,omitempty"`
	BearerTokenEnv      string        `json:"bearer_token_env,omitempty"`
	Timeout             time.Duration `json:"-"`
	TimeoutText         string        `json:"timeout,omitempty"`
}

func (c *ServerConfig) Normalize() error {
	c.Name = strings.TrimSpace(c.Name)
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Pack = strings.TrimSpace(c.Pack)
	c.Entitlement = strings.TrimSpace(c.Entitlement)
	c.FeatureKey = strings.TrimSpace(c.FeatureKey)
	c.BearerTokenEnv = strings.TrimSpace(c.BearerTokenEnv)
	if c.Name == "" || c.Endpoint == "" {
		return fmt.Errorf("MCP name and endpoint are required")
	}
	if c.BearerTokenEnv != "" && strings.TrimSpace(os.Getenv(c.BearerTokenEnv)) == "" {
		return fmt.Errorf("MCP bearer token environment variable %q is empty", c.BearerTokenEnv)
	}
	if strings.TrimSpace(c.TimeoutText) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(c.TimeoutText))
		if err != nil || d <= 0 || d > 5*time.Minute {
			return fmt.Errorf("MCP timeout must be >0 and <=5m")
		}
		c.Timeout = d
	}
	return nil
}

type Manager interface { io.Closer }

func ValidateEndpoint(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil { return nil, fmt.Errorf("parse MCP endpoint: %w", err) }
	if u.Scheme != "https" { return nil, fmt.Errorf("MCP endpoint must use https") }
	if u.User != nil { return nil, fmt.Errorf("MCP endpoint must not contain URL userinfo") }
	host := strings.TrimSpace(u.Hostname())
	if host == "" { return nil, fmt.Errorf("MCP endpoint host is required") }
	if !allowPrivate && strings.EqualFold(host, "localhost") { return nil, fmt.Errorf("private/localhost MCP endpoint requires explicit opt-in") }
	if ip := net.ParseIP(host); ip != nil && !allowPrivate && !publicIP(ip) { return nil, fmt.Errorf("private MCP endpoint requires explicit opt-in") }
	return u, nil
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

func secureHTTPClient(config ServerConfig) (*http.Client, error) {
	endpoint, err := ValidateEndpoint(config.Endpoint, config.AllowPrivateNetwork)
	if err != nil { return nil, err }
	timeout := config.Timeout
	if timeout <= 0 { timeout = 30 * time.Second }
	dialer := &net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil { return nil, err }
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil { return nil, err }
		for _, ip := range ips {
			if !config.AllowPrivateNetwork && !publicIP(ip) { return nil, fmt.Errorf("MCP endpoint resolved to non-public address") }
		}
		if len(ips) == 0 { return nil, fmt.Errorf("MCP endpoint resolved to no addresses") }
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	var roundTripper http.RoundTripper = transport
	if config.BearerTokenEnv != "" {
		roundTripper = bearerTransport{base: transport, originHost: strings.ToLower(endpoint.Hostname()), tokenEnvVar: config.BearerTokenEnv}
	}
	client := &http.Client{Transport: roundTripper, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 { return fmt.Errorf("too many MCP redirects") }
		if _, err := ValidateEndpoint(req.URL.String(), config.AllowPrivateNetwork); err != nil { return err }
		return nil
	}
	return client, nil
}

type bearerTransport struct { base http.RoundTripper; originHost, tokenEnvVar string }
func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if strings.EqualFold(clone.URL.Hostname(), t.originHost) {
		token := strings.TrimSpace(os.Getenv(t.tokenEnvVar))
		if token == "" { return nil, fmt.Errorf("MCP bearer token environment variable %q became empty", t.tokenEnvVar) }
		clone.Header.Set("Authorization", "Bearer "+token)
	} else { clone.Header.Del("Authorization") }
	return t.base.RoundTrip(clone)
}
func minDuration(a,b time.Duration) time.Duration { if a < b { return a }; return b }

func ConnectAndRegister(ctx context.Context, tools *capability.ToolRegistry, resources *capability.ResourceRegistry, configs []ServerConfig) (Manager, error) {
	for i := range configs {
		if err := configs[i].Normalize(); err != nil { return nil, fmt.Errorf("MCP server %q: %w", configs[i].Name, err) }
	}
	return connectAndRegister(ctx, tools, resources, configs)
}
