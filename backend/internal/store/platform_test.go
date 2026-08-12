package store

import (
	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/memory"
	"context"
	"testing"
	"time"
)

func TestDeviceCredentialCarriesTrustedCommercialClaims(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	want := domain.Identity{UserID: "u-commercial", DeviceID: "device-1", TenantID: "tenant-a", Plan: "plus"}
	if err := s.EnrollDevice(ctx, want, "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.AuthenticateDevice(ctx, want.DeviceID, "0123456789abcdef0123456789abcdef")
	if err != nil || !ok {
		t.Fatalf("auth ok=%v err=%v", ok, err)
	}
	if got.UserID != want.UserID || got.DeviceID != want.DeviceID || got.TenantID != want.TenantID || got.Plan != want.Plan {
		t.Fatalf("claims got=%+v want=%+v", got, want)
	}
	if _, ok, err := s.AuthenticateDevice(ctx, want.DeviceID, "wrong-but-long-enough-token"); err != nil || ok {
		t.Fatalf("wrong credential ok=%v err=%v", ok, err)
	}
}

func TestFeatureCatalogVersioningAndSafeExecutionMetadata(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	catalog := controlplane.NewFeatureCatalog(s)
	m := controlplane.FeatureModule{ID: "market.gold", Version: 2, Lifecycle: "beta", Execution: "external", MinProtocol: 3, Implementation: "market-provider-v2"}
	if err := catalog.Put(ctx, m); err != nil {
		t.Fatal(err)
	}
	m.Version = 1
	if err := catalog.Put(ctx, m); err == nil {
		t.Fatal("feature version rollback should be rejected")
	}
	m.Version = 3
	m.Execution = "https://untrusted.example/plugin.so"
	if err := catalog.Put(ctx, m); err == nil {
		t.Fatal("arbitrary execution mode should be rejected")
	}
}

func TestDeviceTwinVersionAndMemorySupersede(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	v := 111
	c := controlplane.RuntimeConfig{VADThreshold: &v}
	before, _ := s.ConfigGeneration(ctx)
	tw, e := s.SetDesired(ctx, "u", "d", c)
	if e != nil || tw.DesiredVersion <= before {
		t.Fatalf("%+v %v", tw, e)
	}
	if e = s.Report(ctx, "u", "d", tw.DesiredVersion, c); e != nil {
		t.Fatal(e)
	}
	m := memory.New(s, memory.HashEmbedding{Dimensions: 32})
	now := time.Now()
	_, _ = m.Remember(ctx, "u", "budget", memory.Temporal, "8m", "user", 1, now.Add(-time.Hour))
	_, _ = m.Remember(ctx, "u", "budget", memory.Temporal, "6m", "user", 1, now)
	xs, e := m.Recall(ctx, "u", "budget", 5)
	if e != nil || len(xs) != 1 || xs[0].Item.Value != "6m" {
		t.Fatalf("%+v %v", xs, e)
	}
}
func TestOutboxTriggerAtomicProjection(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.CreateExpense(ctx, "u", "k", 100, "food", "x", time.Now()); e != nil {
		t.Fatal(e)
	}
	xs, e := s.Claim(ctx, time.Now().Add(time.Second), 10)
	if e != nil || len(xs) == 0 || xs[0].Event.Type != "expense.created" {
		t.Fatalf("%+v %v", xs, e)
	}
}

func TestScopedConfigBumpsGlobalManifestVersion(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	cp := controlplane.New(s, controlplane.RuntimeConfig{Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh"})
	before, err := cp.Manifest(ctx, "u", "d")
	if err != nil {
		t.Fatal(err)
	}
	v := 999
	if err := cp.SetScopedConfig(ctx, "user", "u", controlplane.RuntimeConfig{VADThreshold: &v}); err != nil {
		t.Fatal(err)
	}
	after, err := cp.Manifest(ctx, "u", "d")
	if err != nil {
		t.Fatal(err)
	}
	if after.DesiredVersion <= before.DesiredVersion || after.Desired.VADThreshold == nil || *after.Desired.VADThreshold != 999 {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	if err := s.Report(ctx, "u", "d", after.DesiredVersion, after.Desired); err != nil {
		t.Fatal(err)
	}
}

func TestMarketThresholdTransitionCreatesOneReminderAtomically(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	w, err := s.CreateMarketWatch(ctx, "u", "d", "watch-key", "coingecko", "bitcoin", "USD", ">", 100)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now()
	first, err := s.TriggerMarketWatch(ctx, w, "threshold crossed", when)
	if err != nil || !first {
		t.Fatalf("first=%v err=%v", first, err)
	}
	second, err := s.TriggerMarketWatch(ctx, w, "threshold crossed", when.Add(time.Second))
	if err != nil || second {
		t.Fatalf("second=%v err=%v", second, err)
	}
	xs, err := s.ListReminders(ctx, "u", "d", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 1 {
		t.Fatalf("reminders=%+v", xs)
	}
}
