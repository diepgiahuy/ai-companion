from pathlib import Path
import re


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old[:100]!r}")
    p.write_text(text.replace(old, new, 1))


# Expose exactly one server-owned mutation path so Owner Hub cannot create a
# second settings implementation or report success before desired/applied truth.
p = Path("backend/internal/server/server.go")
text = p.read_text()
needle = '''func (s *Server) isDeviceOnline(deviceID string) bool {
\tif s == nil || s.hub == nil {
\t\treturn false
\t}
\treturn s.hub.get(deviceID) != nil
}
'''
replacement = needle + '''func (s *Server) DeviceOnline(deviceID string) bool { return s.isDeviceOnline(deviceID) }

func (s *Server) UpdateDeviceSettings(ctx context.Context, userID, deviceID string, patch controlplane.RuntimeConfig) (controlplane.Twin, controlplane.SettingsStatus, error) {
\tif s == nil || s.controlPlane == nil {
\t\treturn controlplane.Twin{}, controlplane.SettingsStatus{}, fmt.Errorf("settings control plane unavailable")
\t}
\ttwin, err := s.controlPlane.SetDesired(ctx, userID, deviceID, patch)
\tif err != nil {
\t\treturn controlplane.Twin{}, controlplane.SettingsStatus{}, err
\t}
\ttwin = s.dispatchSettingsUpdate(ctx, userID, deviceID, twin)
\tstatus, err := s.controlPlane.SettingsStatus(ctx, userID, deviceID, s.isDeviceOnline(deviceID))
\tif err != nil {
\t\treturn controlplane.Twin{}, controlplane.SettingsStatus{}, err
\t}
\ttwin.Status = controlplane.TwinStatus(status.State)
\treturn twin, status, nil
}
'''
if text.count(needle) != 1:
    raise SystemExit("server.go: isDeviceOnline block not found exactly once")
text = text.replace(needle, replacement, 1)

start = text.index("func (s *Server) handleTwinPatch(")
end = text.index("func (s *Server) handleConfigSchema", start)
new_patch = '''func (s *Server) handleTwinPatch(w http.ResponseWriter, r *http.Request) {
\tif !s.adminOK(r) {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tuser := strings.TrimSpace(r.URL.Query().Get("user_id"))
\tif user == "" {
\t\tuser = "default"
\t}
\tvar patch controlplane.RuntimeConfig
\tif err := decodeStrictJSON(w, r, 32<<10, &patch); err != nil {
\t\thttp.Error(w, err.Error(), http.StatusBadRequest)
\t\treturn
\t}
\ttwin, status, err := s.UpdateDeviceSettings(r.Context(), user, r.PathValue("deviceID"), patch)
\tif err != nil {
\t\thttp.Error(w, err.Error(), http.StatusBadRequest)
\t\treturn
\t}
\tw.Header().Set("Content-Type", "application/json")
\t_ = json.NewEncoder(w).Encode(struct {
\t\tcontrolplane.Twin
\t\tSettingsStatus controlplane.SettingsStatus `json:"settings_status"`
\t}{Twin: twin, SettingsStatus: status})
}

'''
text = text[:start] + new_patch + text[end:]
p.write_text(text)

# Owner Hub dependencies receive read/live status and the exact same mutation
# closure from the server. No direct second transport or fake success path.
p = Path("backend/internal/ownerweb/dashboard.go")
text = p.read_text()
replace = '''type DeviceSettingsUpdater func(context.Context, string, string, controlplane.RuntimeConfig) (controlplane.Twin, controlplane.SettingsStatus, error)

type Dependencies struct {
\tStore                domain.ReadRepositories
\tControlPlane         *controlplane.Service
\tAuth                 *ownerauth.Service
\tRecordingsDir        string
\tDeviceOnline         func(string) bool
\tUpdateDeviceSettings DeviceSettingsUpdater
}
'''
old = '''type Dependencies struct {
\tStore         domain.ReadRepositories
\tControlPlane  *controlplane.Service
\tAuth          *ownerauth.Service
\tRecordingsDir string
}
'''
if text.count(old) != 1:
    raise SystemExit("dashboard.go: Dependencies block not found exactly once")
text = text.replace(old, replace, 1)

start = text.index("func (h *Handler) handleDevice(")
end = text.index("func (h *Handler) handleClaimDevice", start)
new_device = '''func (h *Handler) handleDevice(w http.ResponseWriter, r *http.Request) {
\tuserID, ok := h.userID(r)
\tif !ok || userID == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tif h.deps.ControlPlane == nil {
\t\thttp.Error(w, "settings control plane unavailable", http.StatusServiceUnavailable)
\t\treturn
\t}
\tdeviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
\tif deviceID == "" {
\t\tdevices, err := h.deps.Store.ListUserDevices(r.Context(), userID, 1)
\t\tif err != nil {
\t\t\thttp.Error(w, "failed to load devices", http.StatusInternalServerError)
\t\t\treturn
\t\t}
\t\tif len(devices) == 0 {
\t\t\th.writeJSON(w, map[string]any{
\t\t\t\t"device_id": "", "connection_status": "offline",
\t\t\t\t"settings_status": controlplane.SettingsStatus{State: controlplane.SettingsUnknown},
\t\t\t})
\t\t\treturn
\t\t}
\t\tdeviceID = devices[0].DeviceID
\t}
\ttwin, err := h.deps.ControlPlane.Manifest(r.Context(), userID, deviceID)
\tif err != nil {
\t\thttp.Error(w, "device not found", http.StatusNotFound)
\t\treturn
\t}
\tonline := h.deps.DeviceOnline != nil && h.deps.DeviceOnline(deviceID)
\tstatus, err := h.deps.ControlPlane.SettingsStatus(r.Context(), userID, deviceID, online)
\tif err != nil {
\t\thttp.Error(w, "failed to load settings status", http.StatusInternalServerError)
\t\treturn
\t}
\tinterval := ""
\tif twin.Desired.OTAPollIntervalSeconds != nil {
\t\tinterval = (time.Duration(*twin.Desired.OTAPollIntervalSeconds) * time.Second).String()
\t}
\th.writeJSON(w, map[string]any{
\t\t"device_id": deviceID,
\t\t"connection_status": map[bool]string{true: "online", false: "offline"}[online],
\t\t"settings_status": status,
\t\t"desired_version": twin.DesiredVersion,
\t\t"reported_version": twin.ReportedVersion,
\t\t"ota_poll_interval": interval,
\t\t"firmware_version": "unknown",
\t\t"wifi_rssi_dbm": nil,
\t})
}

'''
text = text[:start] + new_device + text[end:]

start = text.index("func (h *Handler) handleUpdateDeviceConfig(")
end = text.index("func (h *Handler) handleGetPrivacy", start)
new_update = '''func (h *Handler) handleUpdateDeviceConfig(w http.ResponseWriter, r *http.Request) {
\tuserID, ok := h.userID(r)
\tif !ok || userID == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tif h.deps.UpdateDeviceSettings == nil {
\t\thttp.Error(w, "settings update unavailable", http.StatusServiceUnavailable)
\t\treturn
\t}
\tvar body struct {
\t\tDeviceID        string `json:"device_id"`
\t\tOTAPollInterval string `json:"ota_poll_interval"`
\t}
\tdecoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
\tdecoder.DisallowUnknownFields()
\tif err := decoder.Decode(&body); err != nil {
\t\thttp.Error(w, "invalid request", http.StatusBadRequest)
\t\treturn
\t}
\tvar extra any
\tif err := decoder.Decode(&extra); err != io.EOF {
\t\thttp.Error(w, "request must contain one JSON object", http.StatusBadRequest)
\t\treturn
\t}
\tbody.DeviceID = strings.TrimSpace(body.DeviceID)
\tif body.DeviceID == "" {
\t\thttp.Error(w, "device_id required", http.StatusBadRequest)
\t\treturn
\t}
\tinterval, err := time.ParseDuration(strings.TrimSpace(body.OTAPollInterval))
\tif err != nil || interval <= 0 || interval%time.Second != 0 {
\t\thttp.Error(w, "ota_poll_interval must be a whole-second duration", http.StatusBadRequest)
\t\treturn
\t}
\tseconds64 := int64(interval / time.Second)
\tif seconds64 > int64(^uint(0)>>1) {
\t\thttp.Error(w, "ota_poll_interval is out of range", http.StatusBadRequest)
\t\treturn
\t}
\tseconds := int(seconds64)
\tpatch := controlplane.RuntimeConfig{OTAPollIntervalSeconds: &seconds}
\ttwin, status, err := h.deps.UpdateDeviceSettings(r.Context(), userID, body.DeviceID, patch)
\tif err != nil {
\t\thttp.Error(w, err.Error(), http.StatusBadRequest)
\t\treturn
\t}
\th.writeJSON(w, map[string]any{"ok": true, "twin": twin, "settings_status": status})
}

'''
text = text[:start] + new_update + text[end:]
p.write_text(text)

# Wire Owner Hub to the server-owned live-session settings path.
p = Path("backend/cmd/companiond/owner_auth.go")
text = p.read_text()
old_sig = "func ownerAuthFromEnvironment(next http.Handler, store *pgstore.Store, control *controlplane.Service, claimRepository controlplane.DeviceClaimRepository) http.Handler {"
new_sig = "func ownerAuthFromEnvironment(next http.Handler, store *pgstore.Store, control *controlplane.Service, claimRepository controlplane.DeviceClaimRepository, deviceOnline func(string) bool, updateDeviceSettings ownerweb.DeviceSettingsUpdater) http.Handler {"
if text.count(old_sig) != 1:
    raise SystemExit("owner_auth.go: function signature not found exactly once")
text = text.replace(old_sig, new_sig, 1)
text = text.replace("\t\t\t\tRecordingsDir: recordingsDir,\n", "\t\t\t\tRecordingsDir: recordingsDir,\n\t\t\t\tDeviceOnline: deviceOnline,\n\t\t\t\tUpdateDeviceSettings: updateDeviceSettings,\n")
text = text.replace("\t\t\tRecordingsDir: recordingsDir,\n", "\t\t\tRecordingsDir: recordingsDir,\n\t\t\tDeviceOnline: deviceOnline,\n\t\t\tUpdateDeviceSettings: updateDeviceSettings,\n")
if text.count("UpdateDeviceSettings: updateDeviceSettings") != 2:
    raise SystemExit("owner_auth.go: expected two Owner Hub dependency injections")
p.write_text(text)

p = Path("backend/cmd/companiond/main.go")
text = p.read_text()
old_call = "ownerAuthFromEnvironment(deviceOriginGuard(service.Handler()), data, control, data)"
new_call = "ownerAuthFromEnvironment(deviceOriginGuard(service.Handler()), data, control, data, service.DeviceOnline, service.UpdateDeviceSettings)"
if text.count(old_call) != 1:
    raise SystemExit("main.go: Owner Hub auth wiring call not found exactly once")
p.write_text(text.replace(old_call, new_call, 1))

p = Path("backend/cmd/companiond/owner_auth_test.go")
text = p.read_text()
if text.count("ownerAuthFromEnvironment(next, nil, nil, nil)") != 2:
    raise SystemExit("owner_auth_test.go: expected two old helper calls")
text = text.replace("ownerAuthFromEnvironment(next, nil, nil, nil)", "ownerAuthFromEnvironment(next, nil, nil, nil, nil, nil)")
p.write_text(text)

# Device page must render backend truth instead of hardcoded ONLINE/RSSI/firmware
# fallbacks. This only changes PLAN 07 status/config presentation, not OTA policy.
p = Path("backend/internal/ownerweb/dashboard.html")
text = p.read_text()
replacements = {
    '<div class="block-metric" style="color:var(--accent-green)">ONLINE</div>': '<div class="block-metric">${(data?.connection_status || \'unknown\').toUpperCase()}</div>',
    '<div style="font-size:0.8rem;color:var(--text-muted)">Wi-Fi RSSI: ${data?.wifi_rssi_dbm || -58} dBm</div>': '<div style="font-size:0.8rem;color:var(--text-muted)">Wi-Fi RSSI: ${data?.wifi_rssi_dbm == null ? \'not reported\' : `${data.wifi_rssi_dbm} dBm`}</div>',
    '<div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.4rem">Firmware: ${data?.firmware_version || \'v2.4.0\'}</div>': '<div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.4rem">Firmware: ${data?.firmware_version || \'unknown\'}</div>',
    '<div id="ota-status-banner" style="font-size:0.85rem;color:var(--text-muted);padding:0.6rem 0.8rem;background:#202020;border-radius:6px">\n          Status: Ready. Last verified signed release: <b>v2.4.0 (Stable)</b>\n        </div>': '<div id="ota-status-banner" style="font-size:0.85rem;color:var(--text-muted);padding:0.6rem 0.8rem;background:#202020;border-radius:6px">\n          Settings: <b>${data?.settings_status?.state || \'unknown\'}</b> • desired ${data?.desired_version ?? 0} • reported ${data?.reported_version ?? 0}\n        </div>',
}
for old, new in replacements.items():
    if text.count(old) != 1:
        raise SystemExit(f"dashboard.html: expected one truth-render marker, got {text.count(old)}")
    text = text.replace(old, new, 1)
p.write_text(text)
