idf_component_register(
  SRCS "src/ota_manager.cpp"
  INCLUDE_DIRS "include"
  REQUIRES esp32_network app_update esp_http_client esp-tls mbedtls json
)

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Werror)
