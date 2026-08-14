package pgstore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig keeps PostgreSQL connection policy explicit. Phase B uses this
// package only in integration/migration tooling; product composition remains
// SQLite until the hard-cut phase removes SQLite in one reviewed change.
type PoolConfig struct {
	DSN               string
	MaxConns          int32
	MinConns          int32
	ConnectTimeout    time.Duration
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

func (c PoolConfig) normalized() (PoolConfig, error) {
	c.DSN = strings.TrimSpace(c.DSN)
	if c.DSN == "" {
		return c, errors.New("PostgreSQL DSN is required")
	}
	if err := requireSecurePostgresURL(c.DSN); err != nil {
		return c, err
	}
	if c.MaxConns <= 0 {
		c.MaxConns = 8
	}
	if c.MinConns < 0 || c.MinConns > c.MaxConns {
		return c, fmt.Errorf("invalid PostgreSQL pool bounds min=%d max=%d", c.MinConns, c.MaxConns)
	}
	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.MaxConnLifetime <= 0 {
		c.MaxConnLifetime = 30 * time.Minute
	}
	if c.MaxConnIdleTime <= 0 {
		c.MaxConnIdleTime = 5 * time.Minute
	}
	if c.HealthCheckPeriod <= 0 {
		c.HealthCheckPeriod = 30 * time.Second
	}
	return c, nil
}

func requireSecurePostgresURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return fmt.Errorf("PostgreSQL DSN must be a postgres:// or postgresql:// URL")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if isLoopbackHost(host) {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(parsed.Query().Get("sslmode")))
	switch mode {
	case "require", "verify-ca", "verify-full":
		return nil
	default:
		return fmt.Errorf("remote PostgreSQL requires sslmode=require, verify-ca, or verify-full")
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Open creates and pings a bounded pgx pool. It never runs schema migrations;
// Atlas owns production schema changes.
func Open(ctx context.Context, config PoolConfig) (*pgxpool.Pool, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(normalized.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL DSN: %w", err)
	}
	poolConfig.MaxConns = normalized.MaxConns
	poolConfig.MinConns = normalized.MinConns
	poolConfig.MaxConnLifetime = normalized.MaxConnLifetime
	poolConfig.MaxConnIdleTime = normalized.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = normalized.HealthCheckPeriod
	poolConfig.ConnConfig.ConnectTimeout = normalized.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, normalized.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}
