#include "host_app.h"
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
    CefRefPtr<CefSchemeRegistrar> registrar) {
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
    window_info.SetAsWindowless(0);

    CefBrowserSettings browser_settings;
    browser_settings.javascript_close_windows = STATE_DISABLED;
    browser_settings.javascript_access_clipboard = STATE_ENABLED;
    browser_settings.javascript_dom_paste = STATE_ENABLED;
    browser_settings.local_storage = STATE_ENABLED;
    browser_settings.databases = STATE_ENABLED;
    browser_settings.web_security = STATE_ENABLED;
    browser_settings.remote_fonts = STATE_ENABLED;
    browser_settings.minimum_zoom_level = -2.0f;
    browser_settings.maximum_zoom_level = 2.0f;

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
