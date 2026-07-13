cmake_minimum_required(VERSION 3.16)
project(wails-cef-host CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

set(CEF_ROOT "$ENV{CEF_ROOT}" CACHE PATH "CEF binary distribution root")
if(NOT CEF_ROOT)
    set(CEF_ROOT "$HOME/cef147" CACHE PATH "CEF binary distribution root")
endif()

find_package(PkgConfig REQUIRED)
pkg_check_modules(GTK3 REQUIRED IMPORTED_TARGET gtk+-3.0)
pkg_check_modules(GIO_UNIX_2_0 REQUIRED IMPORTED_TARGET gio-unix-2.0)
pkg_check_modules(X11 REQUIRED IMPORTED_TARGET x11)
pkg_check_modules(JSON REQUIRED IMPORTED_TARGET jsoncpp)

add_executable(wails-cef-host
    src/main.cc
    src/host_app.cc
    src/window_host.cc
    src/ipc_handler.cc
    src/cef_resource_handler.cc
    src/cef_v8_handler.cc
    src/host_adapter.cc
)

target_include_directories(wails-cef-host PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
    ${CEF_ROOT}/include
    ${CEF_ROOT}
)

target_link_libraries(wails-cef-host PRIVATE
    ${CEF_ROOT}/libcef.so
    PkgConfig::GTK3
    PkgConfig::GIO_UNIX_2_0
    PkgConfig::X11
    PkgConfig::JSON
    pthread
    dl
)

target_compile_options(wails-cef-host PRIVATE
    -Wall -Wextra -Wpedantic
    -fno-strict-aliasing -fPIC -fstack-protector -funwind-tables
    -fvisibility=hidden -pthread
    -DNDEBUG
)

install(TARGETS wails-cef-host DESTINATION bin)
