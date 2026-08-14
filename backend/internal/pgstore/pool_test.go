package pgstore

import "testing"

func TestRequireSecurePostgresURL(t *testing.T) {
	for _, raw := range []string{
		"postgres://u:p@127.0.0.1:5432/db?sslmode=disable",
		"postgresql://u:p@localhost/db?sslmode=disable",
		"postgres://u:p@example.com/db?sslmode=require",
		"postgres://u:p@example.com/db?sslmode=verify-full",
	} {
		if err := requireSecurePostgresURL(raw, false); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"",
		"mysql://u:p@example.com/db",
		"postgres://u:p@example.com/db",
		"postgres://u:p@example.com/db?sslmode=disable",
		"postgres://u:p@example.com/db?sslmode=prefer",
	} {
		if err := requireSecurePostgresURL(raw, false); err == nil {
			t.Fatalf("%s: expected fail-closed error", raw)
		}
	}
	if err := requireSecurePostgresURL("postgres://u:p@postgres/db?sslmode=disable", true); err != nil {
		t.Fatalf("explicit non-production insecure remote: %v", err)
	}
	if err := requireSecurePostgresURL("postgres://u:p@postgres/db", true); err == nil {
		t.Fatal("insecure remote opt-in must still require explicit sslmode=disable")
	}
}

func TestPoolConfigBounds(t *testing.T) {
	if _, err := (PoolConfig{DSN:"postgres://u:p@localhost/db?sslmode=disable",MaxConns:2,MinConns:3}).normalized(); err == nil {
		t.Fatal("min > max must fail")
	}
	cfg, err := (PoolConfig{DSN:"postgres://u:p@localhost/db?sslmode=disable"}).normalized()
	if err != nil { t.Fatal(err) }
	if cfg.MaxConns <= 0 || cfg.ConnectTimeout <= 0 || cfg.HealthCheckPeriod <= 0 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}
