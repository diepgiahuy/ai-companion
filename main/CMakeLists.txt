idf_component_register(
  SRCS "app_main.cpp"
  REQUIRES companion_app esp32_board esp32_network esp_timer nvs_flash esp_system
)
target_compile_features(${COMPONENT_LIB} PUBLIC cxx_std_20)
target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra)
