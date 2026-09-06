cmake_minimum_required(VERSION 3.20)
project(ClipboardExchangeWin32 LANGUAGES CXX RC)

# Ship one self-contained executable; no Visual C++ redistributable is required.
set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
if(MSVC)
  # Size optimization produces a compact binary and has proven less prone to
  # unsigned-binary ML false positives than MSVC's default /O2 output.
  string(REGEX REPLACE "/O[012dx]" "/O1" CMAKE_CXX_FLAGS_RELEASE "${CMAKE_CXX_FLAGS_RELEASE}")
endif()

set(APP_VERSION "1.0.0" CACHE STRING "Semantic application version")
if(NOT APP_VERSION MATCHES "^([0-9]+)\.([0-9]+)\.([0-9]+)([-+][0-9A-Za-z.-]+)?$")
  message(FATAL_ERROR "APP_VERSION must be semantic, for example 1.0.0")
endif()
file(WRITE "${CMAKE_CURRENT_BINARY_DIR}/version.rcinc"
  "#define APP_FILE_VERSION ${CMAKE_MATCH_1},${CMAKE_MATCH_2},${CMAKE_MATCH_3},0\n"
  "#define APP_VERSION_STRING \"${APP_VERSION}\"\n")

set(APP_SOURCES
  src/main.cpp src/hotkey.cpp src/settings.cpp src/native_input.cpp
  src/room_client.cpp src/history_cache.cpp src/crypto.cpp src/room_events.cpp
  src/deep_link.cpp resources/app.rc)

add_executable(clipboard-exchange-win32 WIN32 ${APP_SOURCES})
target_compile_features(clipboard-exchange-win32 PRIVATE cxx_std_14)
target_compile_definitions(clipboard-exchange-win32 PRIVATE
  UNICODE _UNICODE NOMINMAX APP_MSVC_BUILD WINVER=0x0A00 _WIN32_WINNT=0x0A00)
target_include_directories(clipboard-exchange-win32 PRIVATE
  src "${CMAKE_CURRENT_BINARY_DIR}")
target_compile_options(clipboard-exchange-win32 PRIVATE /W4 /utf-8)
target_link_libraries(clipboard-exchange-win32 PRIVATE
  comctl32 ole32 oleaut32 shell32 uuid user32 gdi32 winhttp crypt32 bcrypt
  uiautomationcore)

function(set_release_output target)
  set_target_properties(${target} PROPERTIES
    RUNTIME_OUTPUT_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/build"
    RUNTIME_OUTPUT_DIRECTORY_DEBUG "${CMAKE_CURRENT_SOURCE_DIR}/build"
    RUNTIME_OUTPUT_DIRECTORY_RELEASE "${CMAKE_CURRENT_SOURCE_DIR}/build"
    RUNTIME_OUTPUT_DIRECTORY_RELWITHDEBINFO "${CMAKE_CURRENT_SOURCE_DIR}/build")
endfunction()
set_release_output(clipboard-exchange-win32)

enable_testing()
function(add_native_test target)
  add_executable(${target} ${ARGN})
  target_compile_features(${target} PRIVATE cxx_std_14)
  target_compile_definitions(${target} PRIVATE UNICODE _UNICODE NOMINMAX WINVER=0x0A00 _WIN32_WINNT=0x0A00)
  target_include_directories(${target} PRIVATE src)
  target_compile_options(${target} PRIVATE /W4 /utf-8)
  set_release_output(${target})
endfunction()

add_native_test(hotkey-test tests/hotkey_test.cpp src/hotkey.cpp)
target_link_libraries(hotkey-test PRIVATE user32)
add_native_test(settings-test tests/settings_test.cpp src/settings.cpp src/hotkey.cpp)
target_link_libraries(settings-test PRIVATE crypt32 shell32 user32)
add_native_test(history-cache-test tests/history_cache_test.cpp src/history_cache.cpp src/settings.cpp src/hotkey.cpp)
target_link_libraries(history-cache-test PRIVATE crypt32 shell32 user32)
add_native_test(crypto-test tests/crypto_test.cpp src/crypto.cpp)
target_link_libraries(crypto-test PRIVATE crypt32 bcrypt)
add_native_test(room-url-test tests/room_url_test.cpp src/room_client.cpp src/crypto.cpp)
target_link_libraries(room-url-test PRIVATE ole32 winhttp crypt32 bcrypt)
add_native_test(json-unicode-test tests/json_unicode_test.cpp src/crypto.cpp)
target_link_libraries(json-unicode-test PRIVATE ole32 winhttp crypt32 bcrypt)
add_native_test(deep-link-test tests/deep_link_test.cpp src/deep_link.cpp src/room_client.cpp src/crypto.cpp)
target_link_libraries(deep-link-test PRIVATE shell32 ole32 winhttp crypt32 bcrypt)

foreach(target hotkey-test settings-test history-cache-test crypto-test room-url-test deep-link-test json-unicode-test)
  add_test(NAME ${target} COMMAND ${target})
endforeach()

add_native_test(selection-provider WIN32 tests/selection_provider.cpp)
target_link_libraries(selection-provider PRIVATE user32)
add_native_test(native-input-smoke tests/native_input_smoke.cpp src/native_input.cpp)
target_link_libraries(native-input-smoke PRIVATE ole32 oleaut32 uuid user32 uiautomationcore)
add_dependencies(native-input-smoke selection-provider)
add_native_test(lifecycle-smoke tests/lifecycle_smoke.cpp)
target_link_libraries(lifecycle-smoke PRIVATE user32)
add_native_test(picker-latency tests/picker_latency.cpp)
target_link_libraries(picker-latency PRIVATE user32)
add_native_test(room-client-smoke tests/room_client_smoke.cpp src/room_client.cpp src/crypto.cpp)
target_link_libraries(room-client-smoke PRIVATE ole32 winhttp crypt32 bcrypt)
add_native_test(room-events-smoke tests/room_events_smoke.cpp src/room_events.cpp)
target_link_libraries(room-events-smoke PRIVATE winhttp)
