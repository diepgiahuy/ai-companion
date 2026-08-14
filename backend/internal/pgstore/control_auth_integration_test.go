package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/controlplane"
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/domain"
)

func TestPostgresConversationControlAndAuthParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("pg-state-%d", time.Now().UnixNano())
	user := prefix + "-user"
	device := prefix + "-device"

	scope := conversationctx.Scope{UserID: user, ThreadID: "default"}
	if err := store.Append(ctx, prefix+"-turn", scope, "user", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, prefix+"-turn", scope, "user", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, prefix+"-turn", scope, "assistant", "hi"); err != nil {
		t.Fatal(err)
	}
	messages, err := store.Recent(ctx, scope, 10)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}

	before, err := store.ConfigGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locale := "vi-VN"
	twin, err := store.SetDesired(ctx, user, device, controlplane.RuntimeConfig{Locale: locale})
	if err != nil {
		t.Fatal(err)
	}
	if twin.DeviceID != device || twin.UserID != user || twin.Desired.Locale != locale || twin.DesiredVersion <= before {
		t.Fatalf("twin=%+v before=%d", twin, before)
	}
	if _, err := store.GetTwin(ctx, user+"-other", device); err == nil {
		t.Fatal("device owner mismatch was accepted")
	}
	if err := store.Report(ctx, user, device, twin.DesiredVersion+1, controlplane.RuntimeConfig{Locale: locale}); err == nil {
		t.Fatal("reported version ahead of desired was accepted")
	}
	if err := store.Report(ctx, user, device, twin.DesiredVersion, controlplane.RuntimeConfig{Locale: locale}); err != nil {
		t.Fatal(err)
	}

	threshold := 500
	if err := store.SetConfigOverride(ctx, "user", user, controlplane.RuntimeConfig{VADThreshold: &threshold}); err != nil {
		t.Fatal(err)
	}
	override, ok, err := store.GetConfigOverride(ctx, "user", user)
	if err != nil || !ok || override.VADThreshold == nil || *override.VADThreshold != threshold {
		t.Fatalf("override=%+v ok=%v err=%v", override, ok, err)
	}
	after, err := store.ConfigGeneration(ctx)
	if err != nil || after <= twin.DesiredVersion {
		t.Fatalf("generation after=%d desired=%d err=%v", after, twin.DesiredVersion, err)
	}

	flag := controlplane.Flag{Key: prefix + "-flag", Enabled: true, Rollout: 100, Lifecycle: "released", Variants: map[string]string{"mode": "test"}}
	if err := store.SetFlag(ctx, flag); err != nil {
		t.Fatal(err)
	}
	flags, err := store.Flags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundFlag := false
	for _, item := range flags {
		if item.Key == flag.Key {
			foundFlag = item.Enabled && item.Variants["mode"] == "test"
		}
	}
	if !foundFlag {
		t.Fatalf("flag not found: %+v", flags)
	}

	identity := domain.Identity{UserID: user, DeviceID: device, TenantID: "tenant-a", Plan: "test"}
	token := "0123456789abcdef0123456789abcdef"
	if err := store.EnrollDevice(ctx, identity, token); err != nil {
		t.Fatal(err)
	}
	auth, ok, err := store.AuthenticateDevice(ctx, device, token)
	if err != nil || !ok || auth.UserID != user || auth.TenantID != "tenant-a" || auth.Plan != "test" {
		t.Fatalf("auth=%+v ok=%v err=%v", auth, ok, err)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, device, "wrong-wrong-wrong-token"); err != nil || ok {
		t.Fatalf("wrong credential ok=%v err=%v", ok, err)
	}
	if err := store.SetEntitlement(ctx, user, "capability.test", true, nil); err != nil {
		t.Fatal(err)
	}
	if !store.Allowed(ctx, user, "capability.test") {
		t.Fatal("enabled entitlement denied")
	}
	if err := store.RevokeDevice(ctx, device); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.AuthenticateDevice(ctx, device, token); err != nil || ok {
		t.Fatalf("revoked credential ok=%v err=%v", ok, err)
	}
}

func TestPostgresFirmwareAndModuleParity(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	prefix := fmt.Sprintf("pg-fw-%d", now.UnixNano())
	baseVersion := now.UnixNano()

	eligible := controlplane.FirmwareManifest{
		MetadataVersion: baseVersion,
		Version:         "1.2.3",
		Channel:         prefix,
		Board:           "esp32s3",
		ProtocolMin:     2,
		SecurityVersion: 2,
		URL:             "https://example.invalid/fw.bin",
		SHA256:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Size:            123,
		ExpiresAt:       now.Add(time.Hour),
		Signature:       "test",
	}
	if err := store.PutFirmware(ctx, eligible); err != nil {
		t.Fatal(err)
	}
	tooNewProtocol := eligible
	tooNewProtocol.MetadataVersion++
	tooNewProtocol.Version = "1.2.4"
	tooNewProtocol.ProtocolMin = 3
	if err := store.PutFirmware(ctx, tooNewProtocol); err != nil {
		t.Fatal(err)
	}
	rollbackSecurity := eligible
	rollbackSecurity.MetadataVersion += 2
	rollbackSecurity.Version = "1.2.5"
	rollbackSecurity.SecurityVersion = 1
	if err := store.PutFirmware(ctx, rollbackSecurity); err != nil {
		t.Fatal(err)
	}
	expired := eligible
	expired.MetadataVersion += 3
	expired.Version = "1.2.6"
	expired.ExpiresAt = now.Add(-time.Minute)
	if err := store.PutFirmware(ctx, expired); err != nil {
		t.Fatal(err)
	}

	manifest, ok, err := store.LatestFirmware(ctx, prefix, "esp32s3", 2, 2, now)
	if err != nil || !ok || manifest.MetadataVersion != eligible.MetadataVersion {
		t.Fatalf("manifest=%+v ok=%v err=%v", manifest, ok, err)
	}
	if _, ok, err := store.LatestFirmware(ctx, prefix, "esp32s3", 1, 2, now); err != nil || ok {
		t.Fatalf("incompatible protocol unexpectedly eligible: ok=%v err=%v", ok, err)
	}

	module := controlplane.FeatureModule{ID: prefix + ".module", Version: 1, Lifecycle: "beta", Execution: "native", Implementation: "test"}
	if err := store.PutFeatureModule(ctx, module); err != nil {
		t.Fatal(err)
	}
	older := module
	older.Version = 0
	older.Implementation = "rollback"
	if err := store.PutFeatureModule(ctx, older); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := store.FeatureModule(ctx, module.ID)
	if err != nil || !ok || stored.Version != 1 || stored.Implementation != "test" {
		t.Fatalf("module=%+v ok=%v err=%v", stored, ok, err)
	}
}
