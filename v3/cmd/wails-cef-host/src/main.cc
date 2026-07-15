#include <cstdlib>
#include <cstring>
#include <iostream>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

#include <fcntl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <unistd.h>

#include <gio/gunixsocketaddress.h>
#include <glib.h>

#include <cef_app.h>
#include <cef_client.h>
#include <cef_scheme.h>

#include "host_app.h"
#include "window_host.h"
#include "ipc_handler.h"
#include "spawn_sidecar.h"
#include "validate_cef.h"

namespace {

std::string g_socket_path;
std::string g_capability_token;
std::string g_startup_url = "wails://localhost/";
std::string g_assets_dir;
pid_t g_sidecar_pid = -1;
int g_go_sidecar_fd = -1;
bool g_shutdown_requested = false;
bool g_skip_sidecar = false;

std::string GetEnvOr(const char* key, const char* fallback) {
    const char* val = std::getenv(key);
    return val ? std::string(val) : std::string(fallback);
}

bool SetNonBlocking(int fd) {
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0) return false;
    return fcntl(fd, F_SETFL, flags | O_NONBLOCK) >= 0;
}

void ExtractCapabilityAndArgs(int argc, char** argv) {
    auto parse_flag = [](std::string_view arg, const char* prefix) -> std::string {
        std::string pfx = std::string("--") + prefix + "=";
        if (arg.substr(0, pfx.size()) == pfx) {
            return std::string(arg.substr(pfx.size()));
        }
        return {};
    };
    for (int i = 1; i < argc; ++i) {
        std::string_view arg(argv[i]);
        // Handle both --key=value and --key value syntax
        if (arg == "--cef-host-socket" && i + 1 < argc) {
            g_socket_path = argv[++i];
        } else if (auto v = parse_flag(arg, "cef-host-socket"); !v.empty()) {
            g_socket_path = v;
        } else if (arg == "--cef-host-capability" && i + 1 < argc) {
            g_capability_token = argv[++i];
        } else if (auto v = parse_flag(arg, "cef-host-capability"); !v.empty()) {
            g_capability_token = v;
        } else if (arg == "--cef-host-url" && i + 1 < argc) {
            g_startup_url = argv[++i];
        } else if (auto v = parse_flag(arg, "cef-host-url"); !v.empty()) {
            g_startup_url = v;
        } else if (arg == "--cef-host-assets-dir" && i + 1 < argc) {
            g_assets_dir = argv[++i];
        } else if (auto v = parse_flag(arg, "cef-host-assets-dir"); !v.empty()) {
            g_assets_dir = v;
        } else if (arg == "--cef-host-skip-sidecar") {
            g_skip_sidecar = true;
        }
    }
}

void SetupX11() {
    if (std::getenv("GDK_BACKEND") == nullptr) {
        setenv("GDK_BACKEND", "x11", 1);
    }
    if (std::getenv("OZONE_PLATFORM") == nullptr) {
        setenv("OZONE_PLATFORM", "x11", 1);
    }
    unsetenv("WAYLAND_DISPLAY");
}

}  // namespace

int main(int argc, char** argv) {
    ExtractCapabilityAndArgs(argc, argv);

    CefRefPtr<HostApp> app(new HostApp());

    CefMainArgs main_args(argc, argv);

    int exit_code = CefExecuteProcess(main_args, app.get(), nullptr);
    if (exit_code >= 0) {
        return exit_code;
    }

    SetupX11();

    // Validate the CEF distribution before CefInitialize. The bundle script
    // already does this at packaging time, but we re-check at startup so a
    // corrupted or moved bundle fails fast with an actionable error.
    std::string cef_dir = GetEnvOr("CEF_DIR", "");
    if (!cef_dir.empty()) {
        auto validation = wails_cef::ValidateCefDistribution(cef_dir);
        if (!validation.ok) {
            std::cerr << wails_cef::FormatValidationError(validation) << std::endl;
            return 1;
        }
    }

    // M2+M3: spawn the Go sidecar (wails-go-runtime) and run the handshake.
    // This must happen BEFORE CefInitialize so the IPC channel is wired up
    // before the browser starts producing V8 events. The spawn is opt-out
    // via --cef-host-skip-sidecar (used by tests and by the legacy
    // single-process Go app which talks to CEF in-process).
    std::string argv0 = argc > 0 ? std::string(argv[0]) : std::string();
    if (!g_skip_sidecar) {
        auto spawn = wails_cef::SpawnAndHandshake(argv0, g_assets_dir);
        if (!spawn.ok) {
            std::cerr << "wails-cef-host: sidecar spawn failed: "
                      << spawn.error_message << std::endl;
            // We do NOT exit: the host can still run with no IPC, which is
            // useful for testing the CEF/GTK embed loop in isolation.
        } else {
            g_socket_path = spawn.socket_path;
            g_capability_token = spawn.capability;
            g_go_sidecar_fd = spawn.client_fd;
            g_sidecar_pid = spawn.sidecar_pid;
            std::cerr << "wails-cef-host: sidecar ready (pid="
                      << spawn.sidecar_pid << ", socket=" << spawn.socket_path
                      << ")" << std::endl;
            // Pass the sidecar config to HostApp so OnContextInitialized
            // can register the wails:// scheme handler factory.
            app->SetSidecarConfig(spawn.socket_path, spawn.capability, g_assets_dir);
        }
    }

    CefSettings settings;
    settings.multi_threaded_message_loop = false;
    settings.external_message_pump = true;
    settings.no_sandbox = true;
    settings.log_severity = LOGSEVERITY_INFO;
    settings.remote_debugging_port = 9998;


    if (!cef_dir.empty()) {
        std::string resources_dir = cef_dir + "/Resources";
        std::string locales_dir = resources_dir + "/locales";
        CefString(&settings.resources_dir_path) = resources_dir;
        CefString(&settings.locales_dir_path) = locales_dir;
    }

    if (!CefInitialize(main_args, settings, app.get(), nullptr)) {
        std::cerr << "wails-cef-host: CefInitialize failed" << std::endl;
        if (g_sidecar_pid > 0) {
            wails_cef::SendShutdownEnvelope(g_go_sidecar_fd, g_capability_token,
                                            "host_init_failed");
            wails_cef::TerminateSidecar(g_sidecar_pid);
            if (g_go_sidecar_fd >= 0) close(g_go_sidecar_fd);
        }
        return 1;
    }

    gtk_init(&argc, &argv);

    app->CreateBrowserWindow(g_startup_url);


    guint pump_source = g_idle_add_full(
        G_PRIORITY_DEFAULT_IDLE,
        [](gpointer data) -> gboolean {
            CefDoMessageLoopWork();
            return g_shutdown_requested ? G_SOURCE_REMOVE : G_SOURCE_CONTINUE;
        },
        nullptr, nullptr);

    gtk_main();

    g_source_remove(pump_source);

    if (g_sidecar_pid > 0) {
        wails_cef::SendShutdownEnvelope(g_go_sidecar_fd, g_capability_token,
                                        "host_shutdown");
        wails_cef::TerminateSidecar(g_sidecar_pid);
    }
    if (g_go_sidecar_fd >= 0) {
        close(g_go_sidecar_fd);
        g_go_sidecar_fd = -1;
    }
    if (!g_socket_path.empty()) {
        unlink(g_socket_path.c_str());
    }

    CefShutdown();
    return 0;
}
