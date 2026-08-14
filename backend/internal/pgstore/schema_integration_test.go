package pgstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVerifySchemaIntegration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("COMPANION_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("COMPANION_POSTGRES_TEST_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := OpenStore(ctx, PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if err := data.VerifySchema(ctx); err != nil {
		t.Fatal(err)
	}
}
