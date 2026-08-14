package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/pgstore"
	"companion-server/internal/runtimeconfig"
)

func openProductDatabase(ctx context.Context, profile runtimeconfig.Profile) (*pgstore.Store, error) {
	config, err := loadProductDatabaseConfig(profile)
	if err != nil {
		return nil, err
	}
	data, err := pgstore.OpenStore(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := data.VerifySchema(ctx); err != nil {
		data.Close()
		return nil, err
	}
	return data, nil
}

func loadProductDatabaseConfig(profile runtimeconfig.Profile) (pgstore.PoolConfig, error) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_DATABASE_URL"))
	if dsn == "" {
		return pgstore.PoolConfig{}, fmt.Errorf("COMPANION_DATABASE_URL is required; companiond has no SQLite fallback")
	}
	maxConns, err := databaseInt32("COMPANION_POSTGRES_MAX_CONNS", 8)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	minConns, err := databaseInt32("COMPANION_POSTGRES_MIN_CONNS", 0)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	allowInsecure, err := databaseBool("COMPANION_POSTGRES_ALLOW_INSECURE", false)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	if profile == runtimeconfig.ProfileProduction && allowInsecure {
		return pgstore.PoolConfig{}, fmt.Errorf("production profile cannot enable COMPANION_POSTGRES_ALLOW_INSECURE")
	}
	connectTimeout, err := databaseDuration("COMPANION_POSTGRES_CONNECT_TIMEOUT", 5*time.Second)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	maxLifetime, err := databaseDuration("COMPANION_POSTGRES_MAX_CONN_LIFETIME", 30*time.Minute)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	maxIdle, err := databaseDuration("COMPANION_POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	healthPeriod, err := databaseDuration("COMPANION_POSTGRES_HEALTH_CHECK_PERIOD", 30*time.Second)
	if err != nil {
		return pgstore.PoolConfig{}, err
	}
	return pgstore.PoolConfig{
		DSN: dsn, MaxConns: maxConns, MinConns: minConns,
		AllowInsecureRemote: allowInsecure, ConnectTimeout: connectTimeout,
		MaxConnLifetime: maxLifetime, MaxConnIdleTime: maxIdle,
		HealthCheckPeriod: healthPeriod,
	}, nil
}

func databaseInt32(name string, fallback int32) (int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative 32-bit integer", name)
	}
	return int32(value), nil
}

func databaseBool(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be boolean", name)
	}
	return value, nil
}

func databaseDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 || value > 24*time.Hour {
		return 0, fmt.Errorf("%s must be >0 and <=24h", name)
	}
	return value, nil
}
