#include "companion/nimble_pairing_discovery.hpp"

#include "esp_log.h"
#include "host/ble_gap.h"
#include "host/ble_hs.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"

#include <algorithm>
#include <array>
#include <cstring>

namespace companion::pairing {
namespace {
constexpr char kTag[] = "pairing_ble";
constexpr std::array<uint8_t, 4> kMarker{'A', 'C', 'P', '1'};
constexpr char kHex[] = "0123456789abcdef";
NimblePairingDiscovery* g_instance = nullptr;

int hex_nibble(char value) {
  if (value >= '0' && value <= '9') return value - '0';
  if (value >= 'a' && value <= 'f') return value - 'a' + 10;
  if (value >= 'A' && value <= 'F') return value - 'A' + 10;
  return -1;
}

bool decode_session_id(std::string_view input, std::array<uint8_t, 16>& out) {
  if (input.size() != 32) return false;
  for (size_t i = 0; i < out.size(); ++i) {
    const int high = hex_nibble(input[i * 2]);
    const int low = hex_nibble(input[i * 2 + 1]);
    if (high < 0 || low < 0) return false;
    out[i] = static_cast<uint8_t>((high << 4) | low);
  }
  return true;
}

void encode_session_id(const uint8_t* raw, std::array<char, 33>& out) {
  out.fill('\0');
  for (size_t i = 0; i < 16; ++i) {
    out[i * 2] = kHex[(raw[i] >> 4) & 0x0f];
    out[i * 2 + 1] = kHex[raw[i] & 0x0f];
  }
}
} // namespace

bool NimblePairingDiscovery::init() {
  if (initialized_.load()) return true;
  if (g_instance != nullptr && g_instance != this) return false;
  g_instance = this;
  const esp_err_t err = nimble_port_init();
  if (err != ESP_OK) {
    g_instance = nullptr;
    ESP_LOGE(kTag, "nimble init failed: %d", static_cast<int>(err));
    return false;
  }
  ble_hs_cfg.reset_cb = &NimblePairingDiscovery::on_reset;
  ble_hs_cfg.sync_cb = &NimblePairingDiscovery::on_sync;
  initialized_.store(true);
  nimble_port_freertos_init(&NimblePairingDiscovery::host_task);
  return true;
}

bool NimblePairingDiscovery::start(std::string_view discovery_id) {
  std::array<uint8_t, 16> raw{};
  if (!decode_session_id(discovery_id, raw)) return false;
  advertised_name_.fill(0);
  std::copy(kMarker.begin(), kMarker.end(), advertised_name_.begin());
  std::copy(raw.begin(), raw.end(), advertised_name_.begin() + kMarker.size());
  clear_candidate();
  active_.store(true);
  if (!initialized_.load() && !init()) {
    active_.store(false);
    return false;
  }
  if (!ready_.load()) return true;
  if (!start_radio()) {
    active_.store(false);
    return false;
  }
  return true;
}

void NimblePairingDiscovery::stop() {
  active_.store(false);
  if (ready_.load()) {
    if (ble_gap_adv_active()) (void)ble_gap_adv_stop();
    if (ble_gap_disc_active()) (void)ble_gap_disc_cancel();
  }
  clear_candidate();
}

bool NimblePairingDiscovery::best_candidate(DiscoveryCandidate& out) const {
  taskENTER_CRITICAL(&mux_);
  const bool available = best_.sample_count != 0 && best_.discovery_id[0] != '\0';
  if (available) out = best_;
  taskEXIT_CRITICAL(&mux_);
  return available;
}

void NimblePairingDiscovery::clear_candidate() {
  taskENTER_CRITICAL(&mux_);
  best_ = {};
  best_.rssi = -127;
  taskEXIT_CRITICAL(&mux_);
}

void NimblePairingDiscovery::host_task(void*) {
  nimble_port_run();
  nimble_port_freertos_deinit();
}

void NimblePairingDiscovery::on_sync() {
  if (g_instance == nullptr) return;
  uint8_t own_addr_type = 0;
  const int rc = ble_hs_id_infer_auto(0, &own_addr_type);
  if (rc != 0) {
    ESP_LOGE(kTag, "cannot infer BLE address type: %d", rc);
    return;
  }
  g_instance->own_addr_type_ = own_addr_type;
  g_instance->ready_.store(true);
  if (g_instance->active_.load() && !g_instance->start_radio()) {
    g_instance->active_.store(false);
  }
}

void NimblePairingDiscovery::on_reset(int reason) {
  if (g_instance != nullptr) {
    g_instance->ready_.store(false);
    g_instance->active_.store(false);
    g_instance->clear_candidate();
  }
  ESP_LOGW(kTag, "nimble reset: %d", reason);
}

int NimblePairingDiscovery::gap_event(ble_gap_event* event, void* arg) {
  auto* self = static_cast<NimblePairingDiscovery*>(arg);
  if (self == nullptr || event == nullptr) return 0;
  switch (event->type) {
  case BLE_GAP_EVENT_DISC: {
    ble_hs_adv_fields fields{};
    if (ble_hs_adv_parse_fields(&fields, event->disc.data, event->disc.length_data) == 0 &&
        fields.name != nullptr && fields.name_len == self->advertised_name_.size()) {
      self->observe(fields.name, fields.name_len, event->disc.rssi);
    }
    return 0;
  }
  case BLE_GAP_EVENT_ADV_COMPLETE:
  case BLE_GAP_EVENT_DISC_COMPLETE:
    if (self->active_.load()) (void)self->start_radio();
    return 0;
  default:
    return 0;
  }
}

bool NimblePairingDiscovery::start_radio() {
  if (!ready_.load() || !active_.load()) return false;

  if (!ble_gap_adv_active()) {
    ble_hs_adv_fields fields{};
    fields.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
    fields.name = advertised_name_.data();
    fields.name_len = advertised_name_.size();
    fields.name_is_complete = 1;
    int rc = ble_gap_adv_set_fields(&fields);
    if (rc != 0) {
      ESP_LOGE(kTag, "set advertising fields failed: %d", rc);
      return false;
    }
    ble_gap_adv_params adv{};
    adv.conn_mode = BLE_GAP_CONN_MODE_NON;
    adv.disc_mode = BLE_GAP_DISC_MODE_GEN;
    rc = ble_gap_adv_start(own_addr_type_, nullptr, BLE_HS_FOREVER, &adv,
                           &NimblePairingDiscovery::gap_event, this);
    if (rc != 0) {
      ESP_LOGE(kTag, "start advertising failed: %d", rc);
      return false;
    }
  }

  if (!ble_gap_disc_active()) {
    ble_gap_disc_params scan{};
    scan.passive = 1;
    scan.filter_duplicates = 0;
    scan.itvl = 0;
    scan.window = 0;
    scan.filter_policy = 0;
    scan.limited = 0;
    const int rc = ble_gap_disc(own_addr_type_, BLE_HS_FOREVER, &scan,
                                &NimblePairingDiscovery::gap_event, this);
    if (rc != 0) {
      ESP_LOGE(kTag, "start discovery failed: %d", rc);
      (void)ble_gap_adv_stop();
      return false;
    }
  }
  return true;
}

void NimblePairingDiscovery::observe(const uint8_t* payload, uint8_t length, int8_t rssi) {
  if (!active_.load() || payload == nullptr || length != advertised_name_.size() ||
      !std::equal(kMarker.begin(), kMarker.end(), payload)) {
    return;
  }
  if (std::equal(advertised_name_.begin(), advertised_name_.end(), payload)) return;

  std::array<char, 33> candidate{};
  encode_session_id(payload + kMarker.size(), candidate);

  taskENTER_CRITICAL(&mux_);
  const bool same = best_.sample_count != 0 &&
                    std::equal(candidate.begin(), candidate.end(), best_.discovery_id.begin());
  if (same) {
    if (best_.sample_count != UINT32_MAX) ++best_.sample_count;
    if (rssi > best_.rssi) best_.rssi = rssi;
  } else if (best_.sample_count == 0 || rssi > best_.rssi) {
    best_.discovery_id = candidate;
    best_.rssi = rssi;
    best_.sample_count = 1;
  }
  taskEXIT_CRITICAL(&mux_);
}

} // namespace companion::pairing
