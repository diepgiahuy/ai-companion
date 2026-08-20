package ownerweb

import (
	"fmt"
	"strings"
	"time"

	"companion-server/internal/controlplane"
)

type ownerDeviceConfigProjection struct {
	SmartVADEnabled        *bool    `json:"smart_vad_enabled,omitempty"`
	VADThreshold           *int     `json:"vad_threshold,omitempty"`
	VADSilenceMS           *int     `json:"vad_silence_ms,omitempty"`
	VADMinSpeechMS         *int     `json:"vad_min_speech_ms,omitempty"`
	IdleAfterMS            *int     `json:"idle_after_ms,omitempty"`
	AlarmVisibleMS         *int     `json:"alarm_visible_ms,omitempty"`
	OTAPollIntervalSeconds *int     `json:"ota_poll_interval_s,omitempty"`
	WakeModel              string   `json:"wake_model,omitempty"`
	WakeThreshold          *float64 `json:"wake_threshold,omitempty"`
}

type ownerDeviceProjection struct {
	HasDevice        bool                        `json:"has_device"`
	DeviceID         string                      `json:"device_id"`
	ConnectionStatus string                      `json:"connection_status"`
	SettingsStatus   controlplane.SettingsStatus `json:"settings_status"`
	DesiredVersion   int64                       `json:"desired_version"`
	ReportedVersion  int64                       `json:"reported_version"`
	UpdatedAt        *time.Time                  `json:"updated_at,omitempty"`
	Desired          ownerDeviceConfigProjection `json:"desired"`
	Reported         ownerDeviceConfigProjection `json:"reported"`
	FirmwareVersion  *string                     `json:"firmware_version"`
	WiFiRSSIDBm      *int                        `json:"wifi_rssi_dbm"`
}

func projectOwnerDeviceConfig(config controlplane.RuntimeConfig) ownerDeviceConfigProjection {
	physical := controlplane.DeviceReportedConfig(config)
	return ownerDeviceConfigProjection{
		SmartVADEnabled:        physical.SmartVADEnabled,
		VADThreshold:           physical.VADThreshold,
		VADSilenceMS:           physical.VADSilenceMS,
		VADMinSpeechMS:         physical.VADMinSpeechMS,
		IdleAfterMS:            physical.IdleAfterMS,
		AlarmVisibleMS:         physical.AlarmVisibleMS,
		OTAPollIntervalSeconds: physical.OTAPollIntervalSeconds,
		WakeModel:              physical.WakeModel,
		WakeThreshold:          physical.WakeThreshold,
	}
}

func projectOwnerDevice(twin controlplane.Twin, status controlplane.SettingsStatus, online bool) ownerDeviceProjection {
	updatedAt := twin.UpdatedAt.UTC()
	return ownerDeviceProjection{
		HasDevice:        strings.TrimSpace(twin.DeviceID) != "",
		DeviceID:         twin.DeviceID,
		ConnectionStatus: map[bool]string{true: "online", false: "offline"}[online],
		SettingsStatus:   status,
		DesiredVersion:   twin.DesiredVersion,
		ReportedVersion:  twin.ReportedVersion,
		UpdatedAt:        &updatedAt,
		Desired:          projectOwnerDeviceConfig(twin.Desired),
		Reported:         projectOwnerDeviceConfig(twin.Reported),
		FirmwareVersion:  nil,
		WiFiRSSIDBm:      nil,
	}
}

func emptyOwnerDeviceProjection() ownerDeviceProjection {
	return ownerDeviceProjection{
		HasDevice:        false,
		ConnectionStatus: "offline",
		SettingsStatus:   controlplane.SettingsStatus{State: controlplane.SettingsUnknown},
	}
}

type ownerDeviceConfigRequest struct {
	DeviceID        string   `json:"device_id"`
	OTAPollInterval string   `json:"ota_poll_interval,omitempty"`
	SmartVADEnabled *bool    `json:"smart_vad_enabled,omitempty"`
	VADThreshold    *int     `json:"vad_threshold,omitempty"`
	VADSilenceMS    *int     `json:"vad_silence_ms,omitempty"`
	VADMinSpeechMS  *int     `json:"vad_min_speech_ms,omitempty"`
	IdleAfterMS     *int     `json:"idle_after_ms,omitempty"`
	AlarmVisibleMS  *int     `json:"alarm_visible_ms,omitempty"`
	WakeModel       *string  `json:"wake_model,omitempty"`
	WakeThreshold   *float64 `json:"wake_threshold,omitempty"`
}

func (request ownerDeviceConfigRequest) patch() (controlplane.RuntimeConfig, error) {
	var patch controlplane.RuntimeConfig
	if raw := strings.TrimSpace(request.OTAPollInterval); raw != "" {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval <= 0 || interval%time.Second != 0 {
			return controlplane.RuntimeConfig{}, fmt.Errorf("ota_poll_interval must be a whole-second duration")
		}
		seconds64 := int64(interval / time.Second)
		if seconds64 > int64(^uint(0)>>1) {
			return controlplane.RuntimeConfig{}, fmt.Errorf("ota_poll_interval is out of range")
		}
		seconds := int(seconds64)
		patch.OTAPollIntervalSeconds = &seconds
	}
	patch.SmartVADEnabled = request.SmartVADEnabled
	patch.VADThreshold = request.VADThreshold
	patch.VADSilenceMS = request.VADSilenceMS
	patch.VADMinSpeechMS = request.VADMinSpeechMS
	patch.IdleAfterMS = request.IdleAfterMS
	patch.AlarmVisibleMS = request.AlarmVisibleMS
	if request.WakeModel != nil {
		model := strings.TrimSpace(*request.WakeModel)
		switch model {
		case "wn9_hiesp", "disabled":
			patch.WakeModel = model
		default:
			return controlplane.RuntimeConfig{}, fmt.Errorf("wake_model must be wn9_hiesp or disabled")
		}
	}
	patch.WakeThreshold = request.WakeThreshold
	if !hasOwnerDevicePatch(patch) {
		return controlplane.RuntimeConfig{}, fmt.Errorf("no settings provided to update")
	}
	if err := controlplane.Validate(patch); err != nil {
		return controlplane.RuntimeConfig{}, err
	}
	return patch, nil
}

func hasOwnerDevicePatch(patch controlplane.RuntimeConfig) bool {
	return patch.SmartVADEnabled != nil ||
		patch.VADThreshold != nil ||
		patch.VADSilenceMS != nil ||
		patch.VADMinSpeechMS != nil ||
		patch.IdleAfterMS != nil ||
		patch.AlarmVisibleMS != nil ||
		patch.OTAPollIntervalSeconds != nil ||
		patch.WakeModel != "" ||
		patch.WakeThreshold != nil
}
