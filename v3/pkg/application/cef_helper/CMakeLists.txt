cmake_minimum_required(VERSION 3.16)
project(wails_cef_helper LANGUAGES CXX)

# CEF_ROOT must point at the unpacked CEF binary distribution, not merely at
# the runtime directory containing libcef.so. download-cef.sh preserves the
# required include/ and cmake/ trees below CEF_DIR/sdk for this purpose.
set(CEF_ROOT "" CACHE PATH "Unpacked CEF binary distribution")
if(NOT CEF_ROOT)
  message(FATAL_ERROR "Set -DCEF_ROOT=/path/to/cef_binary_... when building wails-cef-helper")
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CEF_RUNTIME_LIBRARY_FLAG "-Wl,-rpath,$ORIGIN" CACHE STRING "Runtime search path")

add_subdirectory(${CEF_ROOT} ${CMAKE_BINARY_DIR}/cef)
# CEF's sample projects create this logical target themselves. The CEF root is
# a subdirectory here, so declare it in this scope as an imported library.
add_library(libcef_lib SHARED IMPORTED)
set_target_properties(libcef_lib PROPERTIES
  IMPORTED_LOCATION "${CEF_ROOT}/Release/libcef.so")

add_executable(wails-cef-helper main.cc)
target_include_directories(wails-cef-helper PRIVATE ${CEF_ROOT})
target_link_libraries(wails-cef-helper PRIVATE libcef_lib libcef_dll_wrapper ${CEF_STANDARD_LIBS})
set_target_properties(wails-cef-helper PROPERTIES
  BUILD_RPATH "$ORIGIN"
  INSTALL_RPATH "$ORIGIN")

install(TARGETS wails-cef-helper RUNTIME DESTINATION .)
