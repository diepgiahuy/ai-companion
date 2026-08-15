#include "companion/esp32_pairing_radio.hpp"

#include "esp_log.h"
#include "esp_timer.h"
#include "host/ble_hs.h"
#include "mbedtls/md.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"

#include <array>
#include <cstdio>
#include <cstring>

namespace companion {
namespace {
constexpr char kTag[] = "pairing_radio";
constexpr char kBase32[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
constexpr size_t kDiscoveryDigestBytes = 10;

bool base32_10(const uint8_t* input, std::array<char, pairing::kDiscoveryIDCapacity>& output) {
  if (input == nullptr) return false;
  output.fill('\0');
  output[0] = 'C'; output[1] = 'P'; output[2] = '-';
  uint32_t accumulator = 0;
  int bits = 0;
  size_t out = 3;
  for (size_t index = 0; index < kDiscoveryDigestBytes; ++index) {
    accumulator = (accumulator << 8) | input[index];
    bits += 8;
    while (bits >= 5) {
      bits -= 5;
      if (out >= 19) return false;
      output[out++] = kBase32[(accumulator >> bits) & 0x1f];
    }
  }
  if (bits != 0 || out != 19) return false; // 10 bytes = exactly 16 base32 symbols.
  output[out] = '\0';
  return true;
}
} // namespace

Esp32PairingRadio* Esp32PairingRadio::instance_ = nullptr;

bool rotating_pairing_discovery_id(std::string_view device_credential,
                                   int64_t unix_seconds,
                                   std::array<char, pairing::kDiscoveryIDCapacity>& output) {
  output.fill('\0');
  if (device_credential.empty() || unix_seconds <= 0) return false;
  const mbedtls_md_info_t* sha256 = mbedtls_md_info_from_type(MBEDTLS_MD_SHA256);
  if (sha256 == nullptr) return false;

  std::array<uint8_t, 32> verifier{};
  if (mbedtls_md(sha256,
                 reinterpret_cast<const unsigned char*>(device_credential.data()),
                 device_credential.size(), verifier.data()) != 0) {
    return false;
  }
  const int64_t slot = unix_seconds / 30;
  std::array<char, 64> message{};
  const int length = std::snprintf(message.data(), message.size(),
                                   "companion-pairing-v1:%lld",
                                   static_cast<long long>(slot));
  if (length <= 0 || static_cast<size_t>(length) >= message.size()) return false;

  std::array<uint8_t, 32> digest{};
  if (mbedtls_md_hmac(sha256, verifier.data(), verifier.size(),
                      reinterpret_cast<const unsigned char*>(message.data()),
                      static_cast<size_t>(length), digest.data()) != 0) {
    return false;
  }
  return base32_10(digest.data(), output);
}

bool Esp32PairingRadio::initialize() {
  if (initialized_.load()) return true;
  if (instance_ != nullptr && instance_ != this) return false;
  queue_ = xQueueCreateStatic(pairing::kMaximumObservations,
                              sizeof(pairing::Observation), queue_bytes_.data(),
                              &queue_storage_);
  if (queue_ == nullptr) return false;
  instance_ = this;
  const esp_err_t result = nimble_port_init();
  if (result != ESP_OK) {
    instance_ = nullptr;
    return false;
  }
  ble_hs_cfg.sync_cb = &Esp32PairingRadio::on_sync;
  nimble_port_freertos_init(&Esp32PairingRadio::host_task);
  initialized_.store(true);
  return true;
}

bool Esp32PairingRadio::start(std::string_view discovery_id, uint32_t duration_ms) {
  if (!initialized_.load() || !synced_.load() || active_.load() ||
      !pairing::valid_discovery_id(discovery_id) || duration_ms == 0) {
    return false;
  }
  xQueueReset(queue_);
  dropped_.store(0);
  if (ble_hs_id_infer_auto(0, &own_address_type_) != 0) return false;

  ble_hs_adv_fields fields{};
  fields.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
  fields.name = reinterpret_cast<uint8_t*>(const_cast<char*>(discovery_id.data()));
  fields.name_len = static_cast<uint8_t>(discovery_id.size());
  fields.name_is_complete = 1;
  if (ble_gap_adv_set_fields(&fields) != 0) return false;

  ble_gap_adv_params adv{};
  adv.conn_mode = BLE_GAP_CONN_MODE_NON;
  adv.disc_mode = BLE_GAP_DISC_MODE_GEN;
  active_.store(true);
  if (ble_gap_adv_start(own_address_type_, nullptr, static_cast<int32_t>(duration_ms),
                        &adv, &Esp32PairingRadio::gap_event, this) != 0) {
    active_.store(false);
    return false;
  }

  ble_gap_disc_params scan{};
  scan.filter_duplicates = 1;
  scan.passive = 1;
  scan.itvl = 0;
  scan.window = 0;
  scan.filter_policy = 0;
  scan.limited = 0;
  if (ble_gap_disc(own_address_type_, static_cast<int32_t>(duration_ms), &scan,
                   &Esp32PairingRadio::gap_event, this) != 0) {
    (void)ble_gap_adv_stop();
    active_.store(false);
    return false;
  }
  ESP_LOGI(kTag, "pairing discovery started for %lu ms", static_cast<unsigned long>(duration_ms));
  return true;
}

void Esp32PairingRadio::stop() {
  if (!initialized_.load()) return;
  (void)ble_gap_disc_cancel();
  (void)ble_gap_adv_stop();
  active_.store(false);
}

bool Esp32PairingRadio::poll(pairing::Observation& observation) {
  return queue_ != nullptr && xQueueReceive(queue_, &observation, 0) == pdTRUE;
}

void Esp32PairingRadio::host_task(void*) {
  nimble_port_run();
  nimble_port_freertos_deinit();
}

void Esp32PairingRadio::on_sync() {
  if (instance_ != nullptr) instance_->synced_.store(true);
}

int Esp32PairingRadio::gap_event(ble_gap_event* event, void* argument) {
  auto* radio = static_cast<Esp32PairingRadio*>(argument);
  if (radio == nullptr || event == nullptr) return 0;
  switch (event->type) {
  case BLE_GAP_EVENT_DISC:
    radio->handle_discovery(event->disc);
    break;
  case BLE_GAP_EVENT_DISC_COMPLETE:
  case BLE_GAP_EVENT_ADV_COMPLETE:
    radio->active_.store(false);
    break;
  default:
    break;
  }
  return 0;
}

void Esp32PairingRadio::handle_discovery(const ble_gap_disc_desc& report) {
  ble_hs_adv_fields fields{};
  if (ble_hs_adv_parse_fields(&fields, report.data, report.length_data) != 0 ||
      fields.name == nullptr || fields.name_len != 19) {
    return;
  }
  const std::string_view id(reinterpret_cast<const char*>(fields.name), fields.name_len);
  if (!pairing::valid_discovery_id(id)) return;

  pairing::Observation observation{};
  std::memcpy(observation.discovery_id.data(), id.data(), id.size());
  observation.discovery_id[id.size()] = '\0';
  observation.rssi = report.rssi;
  observation.last_seen_ms = static_cast<uint64_t>(esp_timer_get_time() / 1000);
  if (xQueueSend(queue_, &observation, 0) != pdTRUE) {
    uint32_t current = dropped_.load();
    if (current != UINT32_MAX) dropped_.store(current + 1);
  }
}

} // namespace companion
