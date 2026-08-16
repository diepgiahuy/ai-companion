package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"companion-server/internal/controlplane"
)

func TestPostgresSettingsDesiredReportedLifecycle(t *testing.T) {
	pool := postgresTestPool(t)
	store, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.VerifySchema(ctx); err != nil {
		t.Fatalf("schema verification failed: %v", err)
	}

	prefix := fmt.Sprintf("settings-%d", time.Now().UnixNano())
	userID, deviceID := prefix+"-owner", prefix+"-device"
	service := controlplane.New(store, controlplane.RuntimeConfig{})
	ptr := func(v int) *int { return &v }

	first, err := service.SetDesired(ctx, userID, deviceID, controlplane.RuntimeConfig{VADThreshold: ptr(600)})
	if err != nil {
		t.Fatal(err)
	}
	if first.DesiredVersion <= 0 {
		t.Fatalf("desired version=%d", first.DesiredVersion)
	}
	status, err := service.SettingsStatus(ctx, userID, deviceID, true)
	if err != nil || status.State != controlplane.SettingsRequested {
		t.Fatalf("requested status=%+v err=%v", status, err)
	}
	status, err = service.SettingsStatus(ctx, userID, deviceID, false)
	if err != nil || status.State != controlplane.SettingsOffline {
		t.Fatalf("offline status=%+v err=%v", status, err)
	}

	rejectedAt := time.Now().UTC().Add(time.Second)
	if err := service.ReportResult(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: first.DesiredVersion, Applied: false, Config: first.Desired,
		FailureCode: "unsupported_setting", ReportedAt: rejectedAt,
	}); err != nil {
		t.Fatal(err)
	}
	status, err = service.SettingsStatus(ctx, userID, deviceID, true)
	if err != nil || status.State != controlplane.SettingsRejected || status.ReportedVersion != 0 || status.FailureCode != "unsupported_setting" {
		t.Fatalf("rejected status=%+v err=%v", status, err)
	}

	appliedAt := rejectedAt.Add(time.Second)
	if err := service.ReportResult(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: first.DesiredVersion, Applied: true, Config: first.Desired, ReportedAt: appliedAt,
	}); err != nil {
		t.Fatal(err)
	}
	status, err = service.SettingsStatus(ctx, userID, deviceID, false)
	if err != nil || status.State != controlplane.SettingsApplied || status.ReportedVersion != first.DesiredVersion || status.FailureCode != "" {
		t.Fatalf("applied status=%+v err=%v", status, err)
	}

	// A contradictory duplicate rejection cannot roll the applied revision back.
	if err := service.ReportResult(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: first.DesiredVersion, Applied: false, Config: first.Desired,
		FailureCode: "late_duplicate", ReportedAt: appliedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	status, _ = service.SettingsStatus(ctx, userID, deviceID, true)
	if status.State != controlplane.SettingsApplied || status.FailureCode != "" {
		t.Fatalf("duplicate rejection rolled state back: %+v", status)
	}

	second, err := service.SetDesired(ctx, userID, deviceID, controlplane.RuntimeConfig{VADThreshold: ptr(700)})
	if err != nil {
		t.Fatal(err)
	}
	if second.DesiredVersion <= first.DesiredVersion {
		t.Fatalf("versions first=%d second=%d", first.DesiredVersion, second.DesiredVersion)
	}
	// Device is still alive but reports its previous applied revision after the
	// new desired write. That is authoritative stale evidence, not "applied".
	if err := service.ReportResult(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: first.DesiredVersion, Applied: true, Config: first.Desired,
		ReportedAt: time.Now().UTC().Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	status, err = service.SettingsStatus(ctx, userID, deviceID, true)
	if err != nil || status.State != controlplane.SettingsStale || status.ReportedVersion != first.DesiredVersion {
		t.Fatalf("stale status=%+v err=%v", status, err)
	}

	if err := service.ReportResult(ctx, userID, deviceID, controlplane.ConfigReportResult{
		Version: second.DesiredVersion + 1, Applied: true, Config: second.Desired,
	}); err == nil {
		t.Fatal("accepted report ahead of desired version")
	}
	if _, err := service.SettingsStatus(ctx, prefix+"-other-owner", deviceID, true); err == nil {
		t.Fatal("wrong owner read settings metadata")
	}

	// A new Store/Service over the same PostgreSQL pool observes the same facts;
	// state is not an in-memory session cache.
	reopened, err := New(pool)
	if err != nil {
		t.Fatal(err)
	}
	reopenedService := controlplane.New(reopened, controlplane.RuntimeConfig{})
	restartedStatus, err := reopenedService.SettingsStatus(ctx, userID, deviceID, true)
	if err != nil || restartedStatus.State != controlplane.SettingsStale || restartedStatus.DesiredVersion != second.DesiredVersion {
		t.Fatalf("restart status=%+v err=%v", restartedStatus, err)
	}
}
