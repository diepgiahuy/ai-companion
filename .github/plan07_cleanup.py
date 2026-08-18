from pathlib import Path
import re


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old[:80]!r}")
    p.write_text(text.replace(old, new, 1))


def regex_once(path: str, pattern: str, replacement: str) -> None:
    p = Path(path)
    text = p.read_text()
    updated, count = re.subn(pattern, replacement, text, count=1, flags=re.S)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one regex match, got {count}: {pattern[:80]!r}")
    p.write_text(updated)


# Firmware: a settings event is valid only for the websocket session that
# received its capability.call. Delete the obsolete config helper entirely.
p = Path("components/esp32_network/src/websocket_voice_backend.cpp")
text = p.read_text()
pattern = r'''(bool WebSocketVoiceBackend::enqueue_settings_event\(const SettingsTwin& settings\) \{.*?event\.type = BackendEventType::settings;\s*)event\.scope = BackendEventScope::global;\s*event\.session_epoch = 0;'''
replacement = r'''\1event.scope = BackendEventScope::session;
  event.session_epoch = session_epoch_.load();'''
text, count = re.subn(pattern, replacement, text, count=1, flags=re.S)
if count != 1:
    raise SystemExit("firmware: settings event scope block not found exactly once")
text, count = re.subn(
    r'''\nbool WebSocketVoiceBackend::enqueue_config_event\(const RuntimeConfigPatch& config\) \{\s*return enqueue_settings_event\(config\);\s*\}\n''',
    "\n",
    text,
    count=1,
    flags=re.S,
)
if count != 1:
    raise SystemExit("firmware: legacy enqueue_config_event block not found exactly once")
p.write_text(text)

# Software-device: remove the temporary compatibility metric and use canonical
# settings terminology in the Tier-1 oracle/evidence.
p = Path("host/companion_software_device/websocket_backend.hpp")
text = p.read_text()
text, count = re.subn(
    r'''\s*// Temporary Tier-1 metric alias while main\.cpp is migrated from the old\s*// config-report assertion to canonical settings capability apply evidence\.\s*union \{\s*uint64_t settings_applies\{\};\s*uint64_t config_reports;\s*\};''',
    "\n    uint64_t settings_applies{};",
    text,
    count=1,
    flags=re.S,
)
if count != 1:
    raise SystemExit("software-device header: temporary config_reports alias not found exactly once")
p.write_text(text)

p = Path("host/companion_software_device/main.cpp")
text = p.read_text()
replacements = {
    "patch_device_config": "patch_device_settings",
    '"config PATCH did not return 200"': '"settings PATCH did not return 200"',
    '"config_reports", stats.config_reports': '"settings_applies", stats.settings_applies',
    '"config_update_report"': '"settings_update_apply"',
    '"live config update was not applied"': '"live settings update was not applied"',
    'stats.config_reports > 0': 'stats.settings_applies > 0',
    '"applied config was not reported to backend"': '"applied settings were not acknowledged to backend"',
    '"config_version"': '"settings_version"',
    'fixture.app.runtime_config_version()': 'fixture.app.settings_version()',
}
for old, new in replacements.items():
    if old not in text:
        raise SystemExit(f"software-device main: missing expected marker {old!r}")
    text = text.replace(old, new)
p.write_text(text)

# Go test harness: the protocol helper must model only current Protocol v2.
replace_once(
    "backend/internal/server/server_test.go",
    "\tUI            any\n\tConfig        *protocol.RuntimeConfig\n\tConfigVersion int64\n\tApplied       bool\n",
    "\tUI            any\n",
)
replace_once(
    "backend/internal/server/server_test.go",
    "\tcase protocol.ConfigReportType:\n\t\tif m.Config == nil {\n\t\t\treturn nil, fmt.Errorf(\"config is required\")\n\t\t}\n\t\treturn protocol.Encode(m.Type, metadata, protocol.ConfigReportPayload{ConfigVersion: m.ConfigVersion, Applied: m.Applied, Config: *m.Config})\n",
    "",
)
replace_once(
    "backend/internal/server/server_test.go",
    "\t\tm.Transport, m.AudioParams = payload.Transport, &payload.AudioParams\n\t\tm.Config, m.ConfigVersion = payload.Config, payload.ConfigVersion\n",
    "\t\tm.Transport, m.AudioParams = payload.Transport, &payload.AudioParams\n",
)
replace_once(
    "backend/internal/server/server_test.go",
    "\tcase protocol.ConfigUpdateType:\n\t\tpayload, err := protocol.DecodePayload[protocol.ConfigUpdatePayload](envelope)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\tm.Config, m.ConfigVersion = &payload.Config, payload.ConfigVersion\n",
    "",
)

# Direct PATCH dispatch and Owner Hub GET use the same durable report/status
# semantics as reconnect reconciliation. A transient process-local rejected flag
# is not accepted as truth.
p = Path("backend/internal/server/server.go")
text = p.read_text()
dispatch_start = text.index("func (s *Server) dispatchSettingsUpdate(")
get_start = text.index("func (s *Server) handleTwinGet", dispatch_start)
strict_start = text.index("func decodeStrictJSON", get_start)
new_dispatch = '''func (s *Server) dispatchSettingsUpdate(ctx context.Context, userID, deviceID string, twin controlplane.Twin) controlplane.Twin {
    online := s.isDeviceOnline(deviceID)
    if !online {
        if status, err := s.controlPlane.SettingsStatus(ctx, userID, deviceID, false); err == nil {
            twin.Status = controlplane.TwinStatus(status.State)
        } else {
            twin.Status = controlplane.TwinStatusOffline
        }
        return twin
    }
    sess := s.hub.get(deviceID)
    if sess == nil || !sess.Supports(devicecap.SettingsName, devicecap.SettingsVersion) {
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    args, err := json.Marshal(devicecap.SettingsArgs{Version: twin.DesiredVersion, Settings: twin.Desired})
    if err != nil {
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()
    res, err := sess.Call(callCtx, devicecap.Call{
        Name: devicecap.SettingsName, Version: devicecap.SettingsVersion,
        Arguments: args, Deadline: time.Now().Add(3 * time.Second),
    })
    if err != nil {
        if s.logger != nil {
            s.logger.Warn("settings dispatch failed", "device_id", deviceID, "error", err)
        }
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    result, err := decodeSettingsResult(res.Value)
    if err != nil {
        if s.logger != nil {
            s.logger.Warn("invalid settings result", "device_id", deviceID, "error", err)
        }
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    if result.Version != twin.DesiredVersion {
        if s.logger != nil {
            s.logger.Warn("device acknowledged unexpected settings version", "device_id", deviceID, "want", twin.DesiredVersion, "got", result.Version)
        }
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    failureCode := strings.TrimSpace(result.Error)
    if !result.Applied && failureCode == "" {
        failureCode = "device_rejected"
    }
    report := controlplane.ConfigReportResult{
        Version: result.Version, Applied: result.Applied, Config: twin.Desired,
        FailureCode: failureCode, ReportedAt: time.Now().UTC(),
    }
    if err := s.controlPlane.ReportResult(ctx, userID, deviceID, report); err != nil {
        if s.logger != nil {
            s.logger.Warn("failed to persist settings outcome", "device_id", deviceID, "version", result.Version, "applied", result.Applied, "error", err)
        }
        twin.Status = controlplane.TwinStatusRequested
        return twin
    }
    if refreshed, err := s.controlPlane.Manifest(ctx, userID, deviceID); err == nil {
        twin = refreshed
    }
    if status, err := s.controlPlane.SettingsStatus(ctx, userID, deviceID, true); err == nil {
        twin.Status = controlplane.TwinStatus(status.State)
    } else if result.Applied {
        twin.Status = controlplane.TwinStatusApplied
    } else {
        twin.Status = controlplane.TwinStatusRejected
    }
    return twin
}
'''
new_get = '''func (s *Server) handleTwinGet(w http.ResponseWriter, r *http.Request) {
    if !s.adminOK(r) {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    user := strings.TrimSpace(r.URL.Query().Get("user_id"))
    if user == "" {
        user = "default"
    }
    deviceID := r.PathValue("deviceID")
    twin, err := s.controlPlane.Manifest(r.Context(), user, deviceID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    status, err := s.controlPlane.SettingsStatus(r.Context(), user, deviceID, s.isDeviceOnline(deviceID))
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    twin.Status = controlplane.TwinStatus(status.State)
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(struct {
        controlplane.Twin
        SettingsStatus controlplane.SettingsStatus `json:"settings_status"`
    }{Twin: twin, SettingsStatus: status})
}

'''
text = text[:dispatch_start] + new_dispatch + new_get + text[strict_start:]
p.write_text(text)

# Fail closed: active source/test surfaces may not retain the deleted names.
active = [
    Path("components/companion_app/include/companion/settings.hpp"),
    Path("components/companion_app/include/companion/mock_backend.hpp"),
    Path("components/companion_app/src/mock_backend.cpp"),
    Path("components/esp32_network/src/websocket_voice_backend.cpp"),
    Path("host/companion_software_device/main.cpp"),
    Path("host/companion_software_device/websocket_backend.hpp"),
    Path("backend/internal/server/server_test.go"),
]
forbidden = [
    "RuntimeConfigPatch",
    "enqueue_config_event",
    "config_reports",
    "ConfigUpdateType",
    "ConfigReportType",
]
for path in active:
    data = path.read_text()
    for marker in forbidden:
        if marker in data:
            raise SystemExit(f"{path}: legacy marker remains: {marker}")
