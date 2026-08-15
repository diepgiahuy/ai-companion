#include "companion/nimble_pairing_discovery.hpp"

#include "esp_log.h"
#include "esp_timer.h"
#include "host/ble_hs.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"

#include <cstring>

namespace companion::pairing {
namespace {
constexpr char kTag[] = "pairing_ble";
}

NimblePairingDiscovery* NimblePairingDiscovery::instance_ = nullptr;

bool NimblePairingDiscovery::init() {
  if (initialized_.load()) return true;
  if (instance_ != nullptr && instance_ != this) return false;

  queue_ = xQueueCreateStatic(kDiscoveryQueueCapacity, sizeof(DiscoveryObservation),
                              queue_buffer_.data(), &queue_storage_);
  if (queue_ == nullptr) return false;

  instance_ = this;
  const int rc = nimble_port_init();
  if (rc != ESP_OK) {
    instance_ = nullptr;
    ESP_LOGE(kTag, "nimble init failed: %d", rc);
    return false;
  }
  ble_hs_cfg.reset_cb = &NimblePairingDiscovery::on_reset;
  ble_hs_cfg.sync_cb = &NimblePairingDiscovery::on_sync;
  nimble_port_freertos_init(&NimblePairingDiscovery::host_task);
  initialized_.store(true);
  return true;
}

bool NimblePairingDiscovery::start(std::string_view local_alias,
                                   uint32_t duration_ms) {
  if (!initialized_.load() || !ready_.load() || active_.load() ||
      !valid_discovery_alias(local_alias) || duration_ms == 0 ||
      duration_ms > 60'000) {
    return false;
  }
  if (ble_gap_adv_active() || ble_gap_disc_active()) return false;
  if (ble_hs_id_infer_auto(0, &own_addr_type_) != 0) return false;

  local_alias_.fill('\0');
  std::memcpy(local_alias_.data(), local_alias.data(), local_alias.size());
  xQueueReset(queue_);
  dropped_.store(0);

  ble_hs_adv_fields fields{};
  fields.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
  fields.name = reinterpret_cast<const uint8_t*>(local_alias_.data());
  fields.name_len = static_cast<uint8_t>(local_alias.size());
  fields.name_is_complete = 1;
  if (ble_gap_adv_set_fields(&fields) != 0) return false;

  ble_gap_adv_params advertising{};
  advertising.conn_mode = BLE_GAP_CONN_MODE_NON;
  advertising.disc_mode = BLE_GAP_DISC_MODE_GEN;
  if (ble_gap_adv_start(own_addr_type_, nullptr, static_cast<int32_t>(duration_ms),
                        &advertising, &NimblePairingDiscovery::gap_event,
                        this) != 0) {
    return false;
  }

  ble_gap_disc_params scanning{};
  scanning.filter_duplicates = 1;
  scanning.passive = 1;
  scanning.filter_policy = 0;
  scanning.limited = 0;
  if (ble_gap_disc(own_addr_type_, static_cast<int32_t>(duration_ms), &scanning,
                   &NimblePairingDiscovery::gap_event, this) != 0) {
    (void)ble_gap_adv_stop();
    return false;
  }

  active_.store(true);
  ESP_LOGI(kTag, "bounded pairing discovery started for %lu ms",
           static_cast<unsigned long>(duration_ms));
  return true;
}

void NimblePairingDiscovery::stop() {
  if (!initialized_.load()) return;
  active_.store(false);
  if (ble_gap_disc_active()) (void)ble_gap_disc_cancel();
  if (ble_gap_adv_active()) (void)ble_gap_adv_stop();
  local_alias_.fill('\0');
}

bool NimblePairingDiscovery::poll(DiscoveryObservation& observation) {
  return queue_ != nullptr && xQueueReceive(queue_, &observation, 0) == pdTRUE;
}

void NimblePairingDiscovery::host_task(void*) {
  nimble_port_run();
  nimble_port_freertos_deinit();
}

void NimblePairingDiscovery::on_sync() {
  if (instance_ != nullptr) instance_->ready_.store(true);
}

void NimblePairingDiscovery::on_reset(int reason) {
  ESP_LOGW(kTag, "nimble reset: %d", reason);
  if (instance_ != nullptr) {
    instance_->ready_.store(false);
    instance_->active_.store(false);
  }
}

int NimblePairingDiscovery::gap_event(ble_gap_event* event, void* arg) {
  auto* discovery = static_cast<NimblePairingDiscovery*>(arg);
  if (discovery == nullptr || event == nullptr) return 0;
  switch (event->type) {
  case BLE_GAP_EVENT_DISC:
    discovery->observe(event->disc);
    break;
  case BLE_GAP_EVENT_DISC_COMPLETE:
  case BLE_GAP_EVENT_ADV_COMPLETE:
    discovery->active_.store(false);
    break;
  default:
    break;
  }
  return 0;
}

void NimblePairingDiscovery::observe(const ble_gap_disc_desc& report) {
  ble_hs_adv_fields fields{};
  if (ble_hs_adv_parse_fields(&fields, report.data, report.length_data) != 0 ||
      fields.name == nullptr || fields.name_len != kDiscoveryAliasLength) {
    return;
  }
  const std::string_view alias(reinterpret_cast<const char*>(fields.name),
                               fields.name_len);
  if (!valid_discovery_alias(alias) || alias == std::string_view(local_alias_.data())) {
    return;
  }

  DiscoveryObservation observation{};
  std::memcpy(observation.discovery_id.data(), alias.data(), alias.size());
  observation.discovery_id[alias.size()] = '\0';
  observation.rssi = report.rssi;
  observation.seen_at_ms = static_cast<uint64_t>(esp_timer_get_time() / 1000);
  if (xQueueSend(queue_, &observation, 0) != pdTRUE) {
    uint32_t current = dropped_.load();
    if (current != UINT32_MAX) dropped_.store(current + 1);
  }
}

} // namespace companion::pairing
