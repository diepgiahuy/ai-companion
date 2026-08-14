cmake_minimum_required(VERSION 3.16)
project(companion_host LANGUAGES CXX)
enable_testing()

add_library(companion_app
  ../components/companion_app/src/app.cpp
  ../components/companion_app/src/wire_protocol.cpp
  ../components/companion_app/src/mock_backend.cpp
)
target_include_directories(companion_app PUBLIC ../components/companion_app/include)
target_compile_features(companion_app PUBLIC cxx_std_20)
target_compile_options(companion_app PRIVATE -Wall -Wextra -Wpedantic -Werror)

add_executable(companion_sim src/sim.cpp)
target_link_libraries(companion_sim PRIVATE companion_app)
target_compile_options(companion_sim PRIVATE -Wall -Wextra -Wpedantic -Werror)

add_executable(companion_tests tests/tests.cpp)
target_link_libraries(companion_tests PRIVATE companion_app)
target_compile_options(companion_tests PRIVATE -Wall -Wextra -Wpedantic -Werror -UNDEBUG)
target_compile_definitions(companion_tests PRIVATE COMPANION_SOURCE_DIR="${CMAKE_SOURCE_DIR}")
add_test(NAME companion_tests COMMAND companion_tests)

add_executable(audio_frontend_tests tests/audio_frontend.cpp)
target_link_libraries(audio_frontend_tests PRIVATE companion_app)
target_compile_options(audio_frontend_tests PRIVATE -Wall -Wextra -Wpedantic -Werror -UNDEBUG)
add_test(NAME audio_frontend_tests COMMAND audio_frontend_tests)

add_executable(protocol_vectors tests/protocol_vectors.cpp)
target_link_libraries(protocol_vectors PRIVATE companion_app)
target_compile_options(protocol_vectors PRIVATE -Wall -Wextra -Wpedantic -Werror -UNDEBUG)
target_compile_definitions(protocol_vectors PRIVATE COMPANION_SOURCE_DIR="${CMAKE_SOURCE_DIR}")
add_test(NAME protocol_vectors COMMAND protocol_vectors)

add_executable(opus_probe tests/opus_probe.cpp)
target_link_libraries(opus_probe PRIVATE ${CMAKE_DL_LIBS})
target_compile_options(opus_probe PRIVATE -Wall -Wextra -Wpedantic -Werror)
add_test(NAME opus_probe COMMAND opus_probe)
set_tests_properties(opus_probe PROPERTIES SKIP_RETURN_CODE 77)
