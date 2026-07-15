#pragma once

#include <include/cef_app.h>
#include <include/cef_client.h>
#include <gtk/gtk.h>
#include <memory>
#include <string>
#include <vector>

class HostApp : public CefApp, public CefBrowserProcessHandler {
public:
    HostApp();
    ~HostApp() override;

    void OnBeforeCommandLineProcessing(
        const CefString& process_type,
        CefRefPtr<CefCommandLine> command_line) override;

    void OnRegisterCustomSchemes(
        CefRawPtr<CefSchemeRegistrar> registrar) override;

    CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override;

    void OnContextInitialized() override;

    CefRefPtr<CefClient> GetDefaultClient() override;

    void CreateBrowserWindow(const std::string& url);
    void CloseBrowserWindow();

    GtkApplicationWindow* GetWindow() const { return window_; }

    void SetSidecarConfig(const std::string& socket_path,
                          const std::string& capability,
                          const std::string& assets_dir) {
        socket_path_ = socket_path;
        capability_ = capability;
        assets_dir_ = assets_dir;
    }

private:
    GtkApplicationWindow* window_ = nullptr;
    CefRefPtr<CefClient> client_;
    CefRefPtr<CefBrowser> browser_;
    std::string socket_path_;
    std::string capability_;
    std::string assets_dir_;

    IMPLEMENT_REFCOUNTING(HostApp);
    DISALLOW_COPY_AND_ASSIGN(HostApp);
};
