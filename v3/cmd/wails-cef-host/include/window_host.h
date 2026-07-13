#pragma once

#include <gtk/gtk.h>
#include <include/cef_client.h>
#include <memory>
#include <string>

class WindowHost {
public:
    WindowHost(GtkApplicationWindow* window, CefRefPtr<CefClient> client);
    ~WindowHost();

    void EmbedBrowser(CefRefPtr<CefBrowser> browser);
    void Close();

    GtkWidget* GetContainer() const { return container_; }
    int GetBrowserId() const { return browser_id_; }

    static WindowHost* FromWidget(GtkWidget* widget);

private:
    static void OnDestroy(GtkWidget* widget, gpointer data);
    static void OnSizeAllocate(GtkWidget* widget, GdkRectangle* allocation, gpointer data);

    GtkApplicationWindow* window_;
    GtkWidget* container_;
    CefRefPtr<CefClient> client_;
    CefRefPtr<CefBrowser> browser_;
    int browser_id_ = 0;
    gulong destroy_handler_id_ = 0;
    gulong size_handler_id_ = 0;

    DISALLOW_COPY_AND_ASSIGN(WindowHost);
};
