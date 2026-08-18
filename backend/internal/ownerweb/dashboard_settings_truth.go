package ownerweb

import (
	"fmt"
	"strings"
)

const optimisticSettingsOutcome = `  try {
    const res = await mutate('/v1/owner/data/device/config', 'POST', { device_id: selectedDeviceID, ota_poll_interval: val });
    const data = await res.json();
    banner.innerHTML = ` + "`✅ Interval updated to <b>${val}</b> for ${selectedDeviceID}.`" + `;
  } catch (e) {
    banner.textContent = 'Failed to update interval';
  }
`

const authoritativeSettingsOutcome = `  try {
    const res = await mutate('/v1/owner/data/device/config', 'POST', { device_id: selectedDeviceID, ota_poll_interval: val });
    let data = null;
    try { data = await res.json(); } catch (_) {}
    if (!res.ok || !data?.ok) {
      banner.textContent = data?.error || ` + "`Failed to update interval (${res.status})`" + `;
      return;
    }
    const state = data?.settings_status?.state || 'requested';
    const desired = data?.settings_status?.desired_version ?? data?.twin?.desired_version ?? '?';
    const reported = data?.settings_status?.reported_version ?? data?.twin?.reported_version ?? '?';
    banner.innerHTML = ` + "`Settings <b>${state}</b> for ${selectedDeviceID} • desired ${desired} • reported ${reported}`" + `;
  } catch (e) {
    banner.textContent = 'Failed to update interval';
  }
`

func init() {
	var err error
	dashboardHTML, err = enforceAuthoritativeSettingsOutcome(dashboardHTML)
	if err != nil {
		panic(err)
	}
}

func enforceAuthoritativeSettingsOutcome(html string) (string, error) {
	if strings.Contains(html, authoritativeSettingsOutcome) && !strings.Contains(html, optimisticSettingsOutcome) {
		return html, nil
	}
	if count := strings.Count(html, optimisticSettingsOutcome); count != 1 {
		return "", fmt.Errorf("owner dashboard settings outcome invariant: optimistic block count=%d", count)
	}
	html = strings.Replace(html, optimisticSettingsOutcome, authoritativeSettingsOutcome, 1)
	if strings.Contains(html, optimisticSettingsOutcome) {
		return "", fmt.Errorf("owner dashboard settings outcome invariant: optimistic success remains")
	}
	return html, nil
}
