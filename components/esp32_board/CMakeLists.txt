idf_component_register(
  SRCS "src/esp32_audio.cpp" "src/gpio_button.cpp" "src/ssd1306_display.cpp"
  INCLUDE_DIRS "include"
  REQUIRES companion_app esp_driver_gpio esp_driver_i2c esp_driver_i2s esp_timer
)
target_compile_features(${COMPONENT_LIB} PUBLIC cxx_std_20)
target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra)
