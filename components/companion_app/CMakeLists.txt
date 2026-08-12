idf_component_register(
  SRCS "src/app.cpp" "src/mock_backend.cpp"
  INCLUDE_DIRS "include"
)
target_compile_features(${COMPONENT_LIB} PUBLIC cxx_std_20)
target_compile_options(${COMPONENT_LIB} PRIVATE -Wall -Wextra -Wpedantic)
