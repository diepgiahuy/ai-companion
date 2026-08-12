idf_component_register(
  SRCS "src/websocket_voice_backend.cpp" "src/wifi_station.cpp"
  INCLUDE_DIRS "include"
  REQUIRES companion_app esp_event esp_netif esp_wifi lwip json nvs_flash esp_websocket_client esp_audio_codec
)

target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Wpedantic -Werror)
