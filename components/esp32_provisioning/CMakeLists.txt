idf_component_register(
  SRCS "src/provisioning_store.cpp" "src/setup_portal.cpp" "src/setup_portal_identity.cpp" "src/claim_client.cpp"
  INCLUDE_DIRS "include"
  REQUIRES nvs_flash esp_event esp_netif esp_wifi esp_http_server esp_http_client esp-tls esp_system mbedtls
)

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Werror)
