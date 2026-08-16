from pathlib import Path
import re


def replace_exact(path: str, old: str, new: str, expected: int = 1) -> None:
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != expected:
        raise SystemExit(f"{path}: expected {expected} exact matches, got {count}: {old!r}")
    p.write_text(text.replace(old, new))


def replace_between(path: str, start: str, end: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    if text.count(start) != 1:
        raise SystemExit(f"{path}: start marker count={text.count(start)} for {start!r}")
    i = text.index(start)
    j = text.find(end, i + len(start))
    if j < 0:
        raise SystemExit(f"{path}: end marker missing for {end!r}")
    p.write_text(text[:i] + new + text[j:])


replace_between(
    "backend/internal/server/server.go",
    "\tcase protocol.ConfigReportType:\n",
    "\tcase protocol.SessionPingType:\n",
    """\tcase protocol.ConfigReportType:
\t\tif s.controlPlane == nil {
\t\t\treturn fmt.Errorf("config reporting unavailable")
\t\t}
\t\tpayload, err := protocol.DecodePayload[protocol.ConfigReportPayload](message)
\t\tif err != nil {
\t\t\treturn err
\t\t}
\t\treturn s.processInbound(message.MessageID, data, func() error {
\t\t\tfailureCode := ""
\t\t\tif !payload.Applied {
\t\t\t\tfailureCode = "device_rejected"
\t\t\t\ts.logger.Warn("device rejected runtime config", "device_id", s.deviceID, "version", payload.ConfigVersion)
\t\t\t}
\t\t\treturn s.controlPlane.ReportResult(ctx, s.userID, s.deviceID, controlplane.ConfigReportResult{
\t\t\t\tVersion: payload.ConfigVersion,
\t\t\t\tApplied: payload.Applied,
\t\t\t\tConfig: controlConfig(payload.Config),
\t\t\t\tFailureCode: failureCode,
\t\t\t\tReportedAt: time.Now().UTC(),
\t\t\t})
\t\t})
""",
)

replace_exact(
    "components/companion_app/src/app.cpp",
    "    if (c.version <= runtime_config_version_) break;",
    """    if (c.version < runtime_config_version_) break;
    if (c.version == runtime_config_version_) {
      backend_.report_config(c, true);
      break;
    }""",
)

replace_exact(
    "backend/internal/ownerweb/dashboard.go",
    "\tControlPlane  *controlplane.Service\n",
    "\tControlPlane     *controlplane.Service\n\tSettingsDelivery DeviceSettingsDelivery\n",
)

replace_between(
    "backend/internal/ownerweb/dashboard.go",
    "func (h *Handler) handleDevice(w http.ResponseWriter, r *http.Request) {\n",
    "\nfunc (h *Handler) handleClaimDevice",
    """func (h *Handler) handleDevice(w http.ResponseWriter, r *http.Request) {
\tuserID, ok := h.userID(r)
\tif !ok || userID == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tdeviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
\tif deviceID == "" {
\t\tdevices, err := h.deps.Store.ListUserDevices(r.Context(), userID)
\t\tif err == nil && len(devices) > 0 {
\t\t\tdeviceID = devices[0].DeviceID
\t\t}
\t}
\tif deviceID == "" {
\t\twriteJSON(w, map[string]any{
\t\t\t"device_id": "none", "status": "offline", "online": false,
\t\t\t"wifi_rssi_dbm": nil, "firmware_version": "unknown",
\t\t\t"sram_budget_kib": nil, "psram_budget_kib": nil,
\t\t\t"settings": controlplane.SettingsStatus{State: controlplane.SettingsUnknown, Online: false},
\t\t})
\t\treturn
\t}
\tif !h.ownsDevice(r.Context(), userID, deviceID) {
\t\thttp.NotFound(w, r)
\t\treturn
\t}
\tif h.deps.ControlPlane == nil {
\t\thttp.Error(w, "device settings unavailable", http.StatusServiceUnavailable)
\t\treturn
\t}
\ttwin, err := h.deps.ControlPlane.Manifest(r.Context(), userID, deviceID)
\tif err != nil {
\t\thttp.Error(w, "device settings unavailable", http.StatusInternalServerError)
\t\treturn
\t}
\tonline := h.deps.SettingsDelivery != nil && h.deps.SettingsDelivery.DeviceOnline(userID, deviceID)
\tsettings, err := h.deps.ControlPlane.SettingsStatus(r.Context(), userID, deviceID, online)
\tif err != nil {
\t\thttp.Error(w, "device settings status unavailable", http.StatusInternalServerError)
\t\treturn
\t}
\tpollInterval := "unknown"
\tif twin.Desired.OTAPollIntervalSeconds != nil && *twin.Desired.OTAPollIntervalSeconds > 0 {
\t\tpollInterval = (time.Duration(*twin.Desired.OTAPollIntervalSeconds) * time.Second).String()
\t}
\tconnection := "offline"
\tif online {
\t\tconnection = "online"
\t}
\twriteJSON(w, map[string]any{
\t\t"device_id": deviceID, "status": connection, "online": online,
\t\t"wifi_rssi_dbm": nil, "firmware_version": "unknown",
\t\t"sram_budget_kib": nil, "psram_budget_kib": nil,
\t\t"ota_poll_interval": pollInterval,
\t\t"desired_config": twin.Desired, "reported_config": twin.Reported,
\t\t"settings": settings,
\t})
}
""",
)

replace_between(
    "backend/internal/ownerweb/dashboard.go",
    "func (h *Handler) handleTriggerOTA(w http.ResponseWriter, r *http.Request) {\n",
    "\nfunc (h *Handler) handleUpdateDeviceConfig",
    """func (h *Handler) handleTriggerOTA(w http.ResponseWriter, r *http.Request) {
\tuserID, ok := h.userID(r)
\tif !ok || userID == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tvar req struct { DeviceID string `json:"device_id"` }
\tdecoder := json.NewDecoder(r.Body)
\tdecoder.DisallowUnknownFields()
\tif err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
\t\thttp.Error(w, "device_id is required", http.StatusBadRequest)
\t\treturn
\t}
\tif !h.ownsDevice(r.Context(), userID, req.DeviceID) {
\t\thttp.NotFound(w, r)
\t\treturn
\t}
\tw.Header().Set("Content-Type", "application/json")
\tw.WriteHeader(http.StatusNotImplemented)
\t_ = json.NewEncoder(w).Encode(map[string]any{
\t\t"ok": false, "device_id": req.DeviceID, "state": "unavailable",
\t\t"message": "Owner Hub OTA dispatch is not implemented; no update was sent",
\t})
}
""",
)

replace_between(
    "backend/internal/ownerweb/dashboard.go",
    "func (h *Handler) handleUpdateDeviceConfig(w http.ResponseWriter, r *http.Request) {\n",
    "\nfunc (h *Handler) handleCreateExpense",
    """func (h *Handler) handleUpdateDeviceConfig(w http.ResponseWriter, r *http.Request) {
\tuserID, ok := h.userID(r)
\tif !ok || userID == "" {
\t\thttp.Error(w, "unauthorized", http.StatusUnauthorized)
\t\treturn
\t}
\tvar req struct {
\t\tDeviceID        string `json:"device_id"`
\t\tOTAPollInterval string `json:"ota_poll_interval"`
\t}
\tdecoder := json.NewDecoder(r.Body)
\tdecoder.DisallowUnknownFields()
\tif err := decoder.Decode(&req); err != nil || strings.TrimSpace(req.DeviceID) == "" {
\t\thttp.Error(w, "invalid device settings payload", http.StatusBadRequest)
\t\treturn
\t}
\tif !h.ownsDevice(r.Context(), userID, req.DeviceID) {
\t\thttp.NotFound(w, r)
\t\treturn
\t}
\tif h.deps.ControlPlane == nil {
\t\thttp.Error(w, "device settings unavailable", http.StatusServiceUnavailable)
\t\treturn
\t}
\tduration, err := time.ParseDuration(strings.TrimSpace(req.OTAPollInterval))
\tif err != nil || duration < time.Hour || duration > 7*24*time.Hour || duration%time.Second != 0 {
\t\thttp.Error(w, "ota_poll_interval must be between 1h and 168h", http.StatusBadRequest)
\t\treturn
\t}
\tseconds := int(duration / time.Second)
\ttwin, err := h.deps.ControlPlane.SetDesired(r.Context(), userID, req.DeviceID, controlplane.RuntimeConfig{OTAPollIntervalSeconds: &seconds})
\tif err != nil {
\t\thttp.Error(w, "device settings rejected", http.StatusBadRequest)
\t\treturn
\t}
\tonline := h.deps.SettingsDelivery != nil && h.deps.SettingsDelivery.DeviceOnline(userID, req.DeviceID)
\tdelivered := 0
\tdeliveryError := ""
\tif online {
\t\tdelivered, err = h.deps.SettingsDelivery.DeliverRuntimeConfig(r.Context(), userID, req.DeviceID, twin)
\t\tif err != nil {
\t\t\tdeliveryError = err.Error()
\t\t}
\t}
\tsettings, statusErr := h.deps.ControlPlane.SettingsStatus(r.Context(), userID, req.DeviceID, online)
\tif statusErr != nil {
\t\thttp.Error(w, "device settings status unavailable", http.StatusInternalServerError)
\t\treturn
\t}
\twriteJSON(w, map[string]any{
\t\t"ok": true, "accepted": true, "device_id": req.DeviceID,
\t\t"ota_poll_interval": duration.String(), "desired_version": twin.DesiredVersion,
\t\t"delivered_sessions": delivered, "delivery_error": deliveryError,
\t\t"settings": settings,
\t})
}
""",
)

p = Path("backend/cmd/companiond/owner_auth.go")
text = p.read_text()
old_sig = "func ownerAuthFromEnvironment(next http.Handler, store *pgstore.Store, control *controlplane.Service, claimRepository controlplane.DeviceClaimRepository) http.Handler {"
new_sig = "func ownerAuthFromEnvironment(next http.Handler, store *pgstore.Store, control *controlplane.Service, claimRepository controlplane.DeviceClaimRepository, settingsDelivery ownerweb.DeviceSettingsDelivery) http.Handler {"
if text.count(old_sig) != 1:
    raise SystemExit(f"owner_auth.go: signature count={text.count(old_sig)}")
text = text.replace(old_sig, new_sig)
pattern = re.compile(r"^(\s*)ControlPlane:\s+control,$", re.M)
text, count = pattern.subn(lambda m: f"{m.group(1)}ControlPlane:     control,\n{m.group(1)}SettingsDelivery: settingsDelivery,", text)
if count != 2:
    raise SystemExit(f"owner_auth.go: expected 2 ControlPlane fields, got {count}")
p.write_text(text)

replace_exact(
    "backend/cmd/companiond/main.go",
    "ownerAuthFromEnvironment(deviceOriginGuard(service.Handler()), data, control, data)",
    "ownerAuthFromEnvironment(deviceOriginGuard(service.Handler()), data, control, data, service)",
)

html = "backend/internal/ownerweb/dashboard.html"
replace_exact(
    html,
    '<div class="block-metric" style="color:var(--accent-green)">ONLINE</div>',
    '<div class="block-metric" style="color:${data?.online ? `var(--accent-green)` : `var(--text-muted)`}">${data?.online ? `ONLINE` : `OFFLINE`}</div>',
)
replace_exact(
    html,
    '<div style="font-size:0.8rem;color:var(--text-muted)">Wi-Fi RSSI: ${data?.wifi_rssi_dbm || -58} dBm</div>',
    '<div style="font-size:0.8rem;color:var(--text-muted)">Wi-Fi RSSI: ${data?.wifi_rssi_dbm == null ? `Unknown` : `${data.wifi_rssi_dbm} dBm`}</div>',
)
replace_exact(
    html,
    '<div class="block-metric">160.5 <span style="font-size:0.9rem;font-weight:400;color:var(--text-muted)">KiB SRAM</span></div>\n          <div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.4rem">PSRAM Codec: 128 KiB</div>',
    '<div class="block-metric">${data?.sram_budget_kib == null ? `Unknown` : data.sram_budget_kib} <span style="font-size:0.9rem;font-weight:400;color:var(--text-muted)">${data?.sram_budget_kib == null ? `` : `KiB SRAM`}</span></div>\n          <div style="font-size:0.8rem;color:var(--text-muted);margin-top:0.4rem">PSRAM: ${data?.psram_budget_kib == null ? `Not reported` : `${data.psram_budget_kib} KiB`}</div>',
)
replace_exact(html, "Firmware: ${data?.firmware_version || 'v2.4.0'}", "Firmware: ${data?.firmware_version || 'unknown'}")
replace_exact(
    html,
    "Manage background update intervals and push signed A/B firmware updates directly to your companion device twin.",
    "Settings state: ${data?.settings?.state || `unknown`}. A requested change is not shown as applied until the device reports it.",
)
replace_exact(
    html,
    '<button class="filter-btn active" style="background:var(--accent-green);color:#000;font-weight:600" onclick="triggerOTA()">🚀 Push Update (v2.4.1)</button>',
    '<button class="filter-btn" disabled title="Owner Hub OTA dispatch is not implemented">🚀 Push unavailable</button>',
)

for temporary in [
    Path(".github/workflows/temporary-settings-codemod.yml"),
    Path("scripts/temporary_settings_codemod.py"),
]:
    if temporary.exists():
        temporary.unlink()
