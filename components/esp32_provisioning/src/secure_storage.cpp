#include "companion/provisioning_store.hpp"

#include "esp_efuse.h"
#include "esp_log.h"
#include "sdkconfig.h"

namespace companion::provisioning {
namespace {
constexpr char kTag[] = "secure_storage";

esp_efuse_block_t configured_hmac_block() {
  static_assert(CONFIG_COMPANION_NVS_HMAC_KEY_ID >= 0 &&
                    CONFIG_COMPANION_NVS_HMAC_KEY_ID <= 5,
                "Companion HMAC eFuse key ID must be 0..5");
  switch (CONFIG_COMPANION_NVS_HMAC_KEY_ID) {
  case 0: return EFUSE_BLK_KEY0;
  case 1: return EFUSE_BLK_KEY1;
  case 2: return EFUSE_BLK_KEY2;
  case 3: return EFUSE_BLK_KEY3;
  case 4: return EFUSE_BLK_KEY4;
  case 5: return EFUSE_BLK_KEY5;
  default: return EFUSE_BLK_KEY_MAX;
  }
}

#if defined(CONFIG_COMPANION_REQUIRE_ENCRYPTED_NVS) && CONFIG_COMPANION_REQUIRE_ENCRYPTED_NVS
constexpr bool kRequireEncryptedNvs = true;
#else
constexpr bool kRequireEncryptedNvs = false;
#endif
} // namespace

bool secure_storage_preflight() {
  // Keep the eFuse lookup path compile-covered in ordinary development builds
  // without enforcing it or changing any eFuse state.
  const esp_efuse_block_t block = configured_hmac_block();
  if (!kRequireEncryptedNvs) return true;

  if (block == EFUSE_BLK_KEY_MAX) {
    ESP_LOGE(kTag, "invalid configured HMAC eFuse key block");
    return false;
  }
  const esp_efuse_purpose_t purpose = esp_efuse_get_key_purpose(block);
  if (purpose != ESP_EFUSE_KEY_PURPOSE_HMAC_UP) {
    // This check intentionally runs before nvs_flash_init(). ESP-IDF's HMAC
    // NVS scheme may generate an HMAC key when the configured block is empty;
    // Companion requires external, deliberate manufacturing provisioning and
    // therefore refuses to let application startup perform that irreversible
    // action implicitly.
    ESP_LOGE(kTag,
             "secure NVS refused: key block %d purpose=%d, expected HMAC_UP; provision eFuse externally before boot",
             CONFIG_COMPANION_NVS_HMAC_KEY_ID,
             static_cast<int>(purpose));
    return false;
  }
  ESP_LOGI(kTag, "secure NVS preflight passed for pre-provisioned HMAC key block %d",
           CONFIG_COMPANION_NVS_HMAC_KEY_ID);
  return true;
}

} // namespace companion::provisioning
