package mcpbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"companion-server/internal/capability"
)

var ErrNotBuilt = errors.New("MCP integration is not included in this build; rebuild with -tags=mcp")

type ServerConfig struct {
	Name                string
	Endpoint            string
	Pack                string
	Entitlement         string
	FeatureKey          string
	AllowPrivateNetwork bool
	Timeout             time.Duration
}

type Manager interface {
	io.Closer
}

func ValidateEndpoint(raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("MCP endpoint must use https")
	}
	if u.User != nil {
		return nil, fmt.Errorf("MCP endpoint must not contain URL userinfo")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return nil, fmt.Errorf("MCP endpoint host is required")
	}
	if !allowPrivate && strings.EqualFold(host, "localhost") {
		return nil, fmt.Errorf("private/localhost MCP endpoint requires explicit opt-in")
	}
	if ip := net.ParseIP(host); ip != nil && !allowPrivate && !publicIP(ip) {
		return nil, fmt.Errorf("private MCP endpoint requires explicit opt-in")
	}
	return u, nil
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())
}

func secureHTTPClient(config ServerConfig) (*http.Client, error) {
	u, err := ValidateEndpoint(config.Endpoint, config.AllowPrivateNetwork)
	if err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: minDuration(timeout, 10*time.Second), KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !config.AllowPrivateNetwork && !publicIP(ip) {
				return nil, fmt.Errorf("MCP endpoint resolved to non-public address")
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("MCP endpoint resolved to no addresses")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many MCP redirects")
		}
		if _, err := ValidateEndpoint(req.URL.String(), config.AllowPrivateNetwork); err != nil {
			return err
		}
		return nil
	}
	_ = u
	return client, nil
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ConnectAndRegister is implemented by build-tag-specific files so importing
// MCP remains an explicit capability boundary rather than leaking SDK types into
// domain/tool packages.
func ConnectAndRegister(ctx context.Context, registry *capability.ToolRegistry, configs []ServerConfig) (Manager, error) {
	return connectAndRegister(ctx, registry, configs)
}
