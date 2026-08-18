idf_component_register(
  SRCS "src/provisioning_store.cpp" "src/secure_storage.cpp" "src/setup_portal.cpp" "src/setup_portal_identity.cpp" "src/claim_client.cpp"
  INCLUDE_DIRS "include"
  REQUIRES companion_app nvs_flash efuse esp_event esp_netif esp_wifi esp_http_server esp_http_client esp-tls esp_system mbedtls
)

if(CONFIG_COMPANION_REQUIRE_ENCRYPTED_NVS)
  if(NOT CONFIG_NVS_ENCRYPTION)
    message(FATAL_ERROR "COMPANION_REQUIRE_ENCRYPTED_NVS requires CONFIG_NVS_ENCRYPTION=y")
  endif()
  if(NOT CONFIG_NVS_SEC_KEY_PROTECT_USING_HMAC)
    message(FATAL_ERROR "COMPANION_REQUIRE_ENCRYPTED_NVS requires HMAC-backed NVS key protection")
  endif()
  if(NOT "${CONFIG_COMPANION_NVS_HMAC_KEY_ID}" STREQUAL "${CONFIG_NVS_SEC_HMAC_EFUSE_KEY_ID}")
    message(FATAL_ERROR "Companion HMAC key slot must match CONFIG_NVS_SEC_HMAC_EFUSE_KEY_ID")
  endif()
endif()

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Werror)