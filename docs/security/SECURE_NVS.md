# Secure persisted credentials (HMAC-backed NVS)

Production-v1 stores Wi-Fi credentials and the enrolled device credential in the default ESP-IDF NVS partition. The normal development build intentionally leaves NVS encryption disabled so development boards are never modified irreversibly by CI or by flashing ordinary firmware.

For production secure storage, Companion uses ESP-IDF NVS AES-XTS encryption with the HMAC peripheral key-protection scheme. The XTS keys are derived at runtime from a 256-bit HMAC key whose eFuse purpose is `HMAC_UP`; the HMAC key itself is not stored in flash.

## Fail-closed build contract

A secure build must enable all of the following:

- `CONFIG_COMPANION_REQUIRE_ENCRYPTED_NVS=y`
- `CONFIG_NVS_ENCRYPTION=y`
- `CONFIG_NVS_SEC_KEY_PROTECT_USING_HMAC=y`
- `CONFIG_COMPANION_NVS_HMAC_KEY_ID=<0..5>`
- `CONFIG_NVS_SEC_HMAC_EFUSE_KEY_ID=<same 0..5>`

CMake rejects a secure Companion build if NVS encryption is disabled, if the HMAC scheme is not selected, or if the Companion and ESP-IDF key-slot settings differ.

The ordinary development build keeps `CONFIG_COMPANION_REQUIRE_ENCRYPTED_NVS` disabled. The eFuse lookup code still compiles, but it does not enforce a slot and never writes eFuses.

## Irreversible-operation guard

ESP-IDF can generate and program an HMAC key when HMAC-backed NVS encryption is initialized with an empty configured key block. Companion deliberately forbids application startup from doing that implicitly.

`provisioning::secure_storage_preflight()` runs before `nvs_flash_init()`. In a secure build it requires the configured physical key block to already report eFuse purpose `ESP_EFUSE_KEY_PURPOSE_HMAC_UP`. Any unused block, mismatched purpose, or invalid configuration stops startup before NVS initialization. The firmware contains no `esp_efuse_write_key()` path.

## Manufacturing / trusted-HIL procedure

Do these steps only on the intended test/production DUT, never from CI:

1. Record the exact firmware SHA, board revision and current eFuse summary.
2. Inspect `KEY0..KEY5` and choose an unused block. Do not assume KEY0 is free just because it is the software default.
3. Generate/store the 256-bit HMAC key using the approved manufacturing secret workflow. Do not place the key in the repository, logs, PRs or evidence artifacts.
4. Program the chosen block with purpose `HMAC_UP` using the reviewed ESP-IDF host workflow.
5. Configure both Companion and ESP-IDF to the chosen key ID and enable the three secure-storage options above.
6. Erase/reprovision the test DUT as required by the migration plan; do not assume an existing plaintext NVS population can be promoted in place.
7. Flash the secure build and verify boot reports the secure-NVS preflight pass without printing any key material.
8. Run onboarding, reboot, Wi-Fi reprovision, credential recovery/rotation and factory-reset HIL. Record only SHA/config fingerprint, DUT/board identity and non-secret pass/fail evidence.

Example host command shape after the block has been deliberately selected and the key file is held outside the repository:

```text
idf.py -p <PORT> efuse-burn-key BLOCK_KEY<n> <HMAC_KEY_FILE> HMAC_UP
```

Treat the eFuse write as irreversible. Review the target block and purpose before executing it.

## Evidence boundary

A successful compile proves only that the guarded software path builds. It does not prove that a physical DUT has encrypted NVS or the expected eFuse state. Issue #170 remains open until trusted hardware verifies the eFuse purpose and encrypted onboarding/recovery lifecycle.
