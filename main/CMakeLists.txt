idf_component_register(
  SRCS "app_main.cpp"
  INCLUDE_DIRS "."
  REQUIRES companion_app esp32_board esp32_network esp32_ota esp32_pairing esp32_provisioning nvs_flash
)
