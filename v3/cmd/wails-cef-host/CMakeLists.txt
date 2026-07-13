cmake_minimum_required(VERSION 3.16)
project(wails-cef-host CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

find_package(PkgConfig REQUIRED)
pkg_check_modules(CEF REQUIRED IMPORTED_TARGET libcef)
pkg_check_modules(GTK4 REQUIRED IMPORTED_TARGET gtk4)
pkg_check_modules(GIO_UNIX_2_0 REQUIRED IMPORTED_TARGET gio-unix-2.0)
pkg_check_modules(X11 REQUIRED IMPORTED_TARGET x11)
pkg_check_modules(JSON REQUIRED IMPORTED_TARGET jsoncpp)

add_executable(wails-cef-host
    src/main.cc
    src/host_app.cc
    src/host_app.h
    src/window_host.cc
    src/window_host.h
    src/ipc_handler.cc
    src/ipc_handler.h
    src/cef_resource_handler.cc
    src/cef_resource_handler.h
    src/cef_v8_handler.cc
    src/cef_v8_handler.h
    src/host_adapter.cc
    src/host_adapter.h
)

target_include_directories(wails-cef-host PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/include
)

target_link_libraries(wails-cef-host PRIVATE
    PkgConfig::CEF
    PkgConfig::GTK4
    PkgConfig::GIO_UNIX_2_0
    PkgConfig::X11
    PkgConfig::JSON
)

target_compile_options(wails-cef-host PRIVATE
    -Wall -Wextra -Wpedantic
)

install(TARGETS wails-cef-host DESTINATION bin)