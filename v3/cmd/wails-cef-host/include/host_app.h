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

private:
    GtkApplicationWindow* window_ = nullptr;
    CefRefPtr<CefClient> client_;
    CefRefPtr<CefBrowser> browser_;

    IMPLEMENT_REFCOUNTING(HostApp);
    DISALLOW_COPY_AND_ASSIGN(HostApp);
};
