package main

import (
	"strings"
	"testing"
	"time"

	"companion-server/internal/runtimeconfig"
)

func TestLoadProductDatabaseConfigRequiresPostgresURL(t *testing.T) {
	t.Setenv("COMPANION_DATABASE_URL", "")
	_, err := loadProductDatabaseConfig(runtimeconfig.ProfileTest)
	if err == nil || !strings.Contains(err.Error(), "no SQLite fallback") {
		t.Fatalf("missing database URL error = %v", err)
	}
}

func TestLoadProductDatabaseConfig(t *testing.T) {
	t.Setenv("COMPANION_DATABASE_URL", "postgres://u:p@postgres/db?sslmode=disable")
	t.Setenv("COMPANION_POSTGRES_ALLOW_INSECURE", "true")
	t.Setenv("COMPANION_POSTGRES_MAX_CONNS", "12")
	t.Setenv("COMPANION_POSTGRES_MIN_CONNS", "2")
	t.Setenv("COMPANION_POSTGRES_CONNECT_TIMEOUT", "7s")
	config, err := loadProductDatabaseConfig(runtimeconfig.ProfileTest)
	if err != nil {
		t.Fatal(err)
	}
	if config.DSN == "" || config.MaxConns != 12 || config.MinConns != 2 || !config.AllowInsecureRemote || config.ConnectTimeout != 7*time.Second {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadProductDatabaseConfigRejectsProductionInsecure(t *testing.T) {
	t.Setenv("COMPANION_DATABASE_URL", "postgres://u:p@postgres/db?sslmode=disable")
	t.Setenv("COMPANION_POSTGRES_ALLOW_INSECURE", "true")
	_, err := loadProductDatabaseConfig(runtimeconfig.ProfileProduction)
	if err == nil || !strings.Contains(err.Error(), "production profile") {
		t.Fatalf("production insecure error = %v", err)
	}
}

func TestLoadProductDatabaseConfigRejectsInvalidPoolSettings(t *testing.T) {
	t.Setenv("COMPANION_DATABASE_URL", "postgres://u:p@localhost/db?sslmode=disable")
	t.Setenv("COMPANION_POSTGRES_MAX_CONNS", "many")
	if _, err := loadProductDatabaseConfig(runtimeconfig.ProfileTest); err == nil {
		t.Fatal("invalid pool setting must fail")
	}
}
