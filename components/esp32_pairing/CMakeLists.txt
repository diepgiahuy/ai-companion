idf_component_register(
  SRCS "src/nimble_pairing_discovery.cpp"
  INCLUDE_DIRS "include"
  REQUIRES bt esp_timer
)

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Werror)
