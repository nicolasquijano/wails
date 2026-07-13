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
        CefRefPtr<CefSchemeRegistrar> registrar) override;

    CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override;

    void OnContextInitialized() override;

    void OnSchedulePrintJob(const CefString& cookie_name,
                           const CefString& job_id) override {}

    void OnPdfPrintFinished(const CefString& cookie_name,
                           bool success) override {}

    bool OnBeforePrintJob(CefRefPtr<CefBrowser> browser,
                         const CefString& cookie_name,
                         CefRefPtr<CefPrintHandler> handler) override {
        return false;
    }

    void OnPrintOptions(CefRefPtr<CefBrowser> browser,
                       CefRefPtr<CefPrintOptions> options) override {}

    void GetPrintSettings(CefRefPtr<CefPrintSettings> settings) override {}

    void RunFileDialog(CefRefPtr<CefBrowser> browser,
                      const CefString& cookie_name) override {}

    void OnResetPrintState(CefRefPtr<CefBrowser> browser) override {}

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
