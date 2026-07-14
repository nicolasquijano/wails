#include "host_app.h"
#include "host_client.h"
#include "window_host.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <iostream>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

#include <gio/gunixsocketaddress.h>
#include <glib.h>

#include <gtk/gtk.h>

#include <cef_app.h>
#include <cef_browser.h>
#include <cef_client.h>
#include <cef_command_line.h>
#include <cef_frame.h>
#include <cef_scheme.h>

namespace {

constexpr int kSchemeOptionStandard = 0x01;
constexpr int kSchemeOptionCORSEnabled = 0x10;
constexpr int kSchemeOptionFetchEnabled = 0x40;
constexpr int kSchemeOptionSecure = 0x08;

std::string GetEnvOr(const char* key, const char* fallback) {
    const char* val = std::getenv(key);
    return val ? std::string(val) : std::string(fallback);
}

}  // namespace

HostApp::HostApp() = default;

HostApp::~HostApp() = default;

void HostApp::OnBeforeCommandLineProcessing(
    const CefString& process_type,
    CefRefPtr<CefCommandLine> command_line) {

    if (!command_line) return;

    command_line->AppendSwitchWithValue("ozone-platform", "x11");
    command_line->AppendSwitchWithValue("runtime-style", "alloy");
    command_line->AppendSwitch("disable-gpu");
    command_line->AppendSwitch("in-process-gpu");
    command_line->AppendSwitchWithValue("lang", "en-US");
    command_line->AppendSwitchWithValue("remote-debugging-port", "9999");

    std::string cef_dir = GetEnvOr("CEF_DIR", "");
    if (!cef_dir.empty()) {
        std::string resources_dir = cef_dir + "/Resources";
        std::string locales_dir = resources_dir + "/locales";
        command_line->AppendSwitchWithValue("resources-dir-path", resources_dir);
        command_line->AppendSwitchWithValue("locales-dir-path", locales_dir);
    }
}

void HostApp::OnRegisterCustomSchemes(
    CefRawPtr<CefSchemeRegistrar> registrar) {
    if (!registrar) return;

    int options = kSchemeOptionStandard | kSchemeOptionCORSEnabled |
                  kSchemeOptionFetchEnabled | kSchemeOptionSecure;
    registrar->AddCustomScheme("wails", options);
}

CefRefPtr<CefBrowserProcessHandler> HostApp::GetBrowserProcessHandler() {
    return this;
}

void HostApp::OnContextInitialized() {
    if (!browser_) {
        CreateBrowserWindow("wails://localhost/");
    }
}

CefRefPtr<CefClient> HostApp::GetDefaultClient() {
    return client_;
}

void HostApp::CreateBrowserWindow(const std::string& url) {
    CefWindowInfo window_info;
    // On Linux/X11, CEF 147 exposes SetAsChild and SetAsWindowless only;
    // SetAsPopup is Windows-only. We use SetAsChild with parent=0, which
    // causes CEF to create a top-level X11 window. The window is then
    // reparented into the GTK host widget via XReparentWindow (see
    // Decision C3) — the GTK visual mismatch that fails when we pass
    // CefWindowInfo.ParentWindow does not apply here because CEF creates
    // the window with its own default visual.
    CefRect bounds(0, 0, 1280, 768);
    window_info.SetAsChild(0, bounds);

    CefBrowserSettings browser_settings;
    browser_settings.javascript_close_windows = STATE_DISABLED;
    browser_settings.javascript_access_clipboard = STATE_ENABLED;
    browser_settings.javascript_dom_paste = STATE_ENABLED;
    browser_settings.local_storage = STATE_ENABLED;
    browser_settings.databases_deprecated = STATE_ENABLED;
    browser_settings.remote_fonts = STATE_ENABLED;

    client_ = new HostClient();

    if (!CefBrowserHost::CreateBrowser(window_info, client_, url,
                                       browser_settings, nullptr, nullptr)) {
        std::cerr << "wails-cef-host: CreateBrowser failed" << std::endl;
    }
}

void HostApp::CloseBrowserWindow() {
    if (browser_) {
        browser_->GetHost()->CloseBrowser(true);
        browser_ = nullptr;
    }
}
