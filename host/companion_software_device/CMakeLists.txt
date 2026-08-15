cmake_minimum_required(VERSION 3.16)
project(companion_software_device LANGUAGES CXX)

find_package(Boost 1.74 REQUIRED COMPONENTS system)
find_package(nlohmann_json 3.11.2 REQUIRED)
find_package(Threads REQUIRED)
find_package(OpenSSL REQUIRED)
find_package(PkgConfig REQUIRED)
pkg_check_modules(OPUS REQUIRED IMPORTED_TARGET opus)
pkg_check_modules(OPUSFILE REQUIRED IMPORTED_TARGET opusfile)

add_executable(companion_software_device
  main.cpp
  websocket_backend.cpp
  ../../components/companion_app/src/app.cpp
  ../../components/companion_app/src/wire_protocol.cpp
)
target_include_directories(companion_software_device PRIVATE
  .
  ../../components/companion_app/include
)
target_compile_features(companion_software_device PRIVATE cxx_std_20)
target_compile_options(companion_software_device PRIVATE -Wall -Wextra -Wpedantic -Werror)
target_compile_definitions(companion_software_device PRIVATE BOOST_ASIO_NO_DEPRECATED)
target_link_libraries(companion_software_device PRIVATE
  Boost::system
  nlohmann_json::nlohmann_json
  PkgConfig::OPUS
  PkgConfig::OPUSFILE
  OpenSSL::Crypto
  Threads::Threads
)
