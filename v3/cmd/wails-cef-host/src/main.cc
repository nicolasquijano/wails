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

namespace {

std::string g_socket_path;
std::string g_capability_token;
std::string g_startup_url = "wails://localhost/";
int g_go_sidecar_fd = -1;
bool g_shutdown_requested = false;

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
    for (int i = 1; i < argc; ++i) {
        std::string_view arg(argv[i]);
        if (arg == "--cef-host-socket" && i + 1 < argc) {
            g_socket_path = argv[++i];
        } else if (arg == "--cef-host-capability" && i + 1 < argc) {
            g_capability_token = argv[++i];
        } else if (arg == "--cef-host-url" && i + 1 < argc) {
            g_startup_url = argv[++i];
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

    CefSettings settings;
    settings.multi_threaded_message_loop = false;
    settings.external_message_pump = true;
    settings.no_sandbox = true;
    settings.log_severity = LOGSEVERITY_INFO;
    settings.remote_debugging_port = 9999;

    std::string cef_dir = GetEnvOr("CEF_DIR", "");
    if (!cef_dir.empty()) {
        std::string resources_dir = cef_dir + "/Resources";
        std::string locales_dir = resources_dir + "/locales";
        CefString(&settings.resources_dir_path) = resources_dir;
        CefString(&settings.locales_dir_path) = locales_dir;
    }

    if (!CefInitialize(main_args, settings, app.get(), nullptr)) {
        std::cerr << "wails-cef-host: CefInitialize failed" << std::endl;
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

    if (g_go_sidecar_fd >= 0) {
        close(g_go_sidecar_fd);
    }

    CefShutdown();
    return 0;
}
