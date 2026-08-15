idf_component_register(
  SRCS "src/nimble_pairing_discovery.cpp" "src/pairing_controller.cpp"
  INCLUDE_DIRS "include"
  REQUIRES bt esp_timer companion_app esp32_network
)

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Werror)
