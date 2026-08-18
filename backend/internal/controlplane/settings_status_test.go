package controlplane

import (
	"testing"
	"time"
)

func TestDeriveSettingsState(t *testing.T) {
	now := time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC)
	later := now.Add(time.Second)
	tests := []struct {
		name   string
		meta   SettingsMetadata
		online bool
		want   SettingsState
	}{
		{name: "unknown", meta: SettingsMetadata{}, online: false, want: SettingsUnknown},
		{name: "requested online", meta: SettingsMetadata{DesiredVersion: 2, ReportedVersion: 1, LastReportVersion: 1, LastReportState: SettingsApplied, DesiredAt: now}, online: true, want: SettingsRequested},
		{name: "pending offline", meta: SettingsMetadata{DesiredVersion: 2, ReportedVersion: 1, LastReportVersion: 1, LastReportState: SettingsApplied, DesiredAt: now}, online: false, want: SettingsOffline},
		{name: "stale post desired report", meta: SettingsMetadata{DesiredVersion: 3, ReportedVersion: 2, LastReportVersion: 2, LastReportState: SettingsApplied, DesiredAt: now, ReportedAt: &later}, online: true, want: SettingsStale},
		{name: "rejected desired", meta: SettingsMetadata{DesiredVersion: 4, ReportedVersion: 3, LastReportVersion: 4, LastReportState: SettingsRejected, DesiredAt: now, ReportedAt: &later}, online: true, want: SettingsRejected},
		{name: "rejected remains truthful offline", meta: SettingsMetadata{DesiredVersion: 4, ReportedVersion: 3, LastReportVersion: 4, LastReportState: SettingsRejected, DesiredAt: now, ReportedAt: &later}, online: false, want: SettingsRejected},
		{name: "applied online", meta: SettingsMetadata{DesiredVersion: 5, ReportedVersion: 5, LastReportVersion: 5, LastReportState: SettingsApplied, DesiredAt: now, ReportedAt: &later}, online: true, want: SettingsApplied},
		{name: "applied remains fact offline", meta: SettingsMetadata{DesiredVersion: 5, ReportedVersion: 5, LastReportVersion: 5, LastReportState: SettingsApplied, DesiredAt: now, ReportedAt: &later}, online: false, want: SettingsApplied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveSettingsState(tt.meta, tt.online); got != tt.want {
				t.Fatalf("state=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestDeviceReportedConfigExcludesBackendAndPendingWakeFields(t *testing.T) {
	threshold := 700
	desired := RuntimeConfig{
		VADThreshold: &threshold,
		Locale:       "vi-VN",
		Timezone:     "Asia/Ho_Chi_Minh",
		VoiceKey:     "voice-a",
		WakeModel:    "pending-plan07b-model",
	}
	reported := DeviceReportedConfig(desired)
	if reported.VADThreshold == nil || *reported.VADThreshold != threshold {
		t.Fatalf("device-owned threshold lost: %+v", reported)
	}
	if reported.Locale != "" || reported.Timezone != "" || reported.VoiceKey != "" || reported.WakeModel != "" {
		t.Fatalf("non-device-applied fields leaked into reported config: %+v", reported)
	}
}
