#include "window_host.h"

#include <cstdio>
#include <cstdlib>
#include <memory>
#include <unordered_map>

#include <gtk/gtkx.h>
#include <gtk/gtk.h>

#include <cef_client.h>
#include <cef_browser.h>
#include <cef_frame.h>
#include <cef_task.h>

namespace {

std::unordered_map<GtkWidget*, WindowHost*>& GlobalWidgetMap() {
    static std::unordered_map<GtkWidget*, WindowHost*> map;
    return map;
}

}  // namespace

WindowHost* WindowHost::FromWidget(GtkWidget* widget) {
    auto it = GlobalWidgetMap().find(widget);
    if (it != GlobalWidgetMap().end()) {
        return it->second;
    }
    GtkWidget* parent = gtk_widget_get_parent(widget);
    if (parent) {
        return FromWidget(parent);
    }
    return nullptr;
}

WindowHost::WindowHost(GtkApplicationWindow* window, CefRefPtr<CefClient> client)
    : window_(window), client_(client) {

    container_ = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    gtk_widget_set_vexpand(container_, TRUE);
    gtk_widget_set_hexpand(container_, TRUE);

    gtk_container_add(GTK_CONTAINER(window), container_);
    gtk_widget_show(container_);

    destroy_handler_id_ = g_signal_connect(
        G_OBJECT(window), "destroy",
        G_CALLBACK(+[](GtkWidget*, gpointer data) {
            auto* self = static_cast<WindowHost*>(data);
            delete self;
        }), this);

    GlobalWidgetMap()[container_] = this;
}

WindowHost::~WindowHost() {
    GlobalWidgetMap().erase(container_);

    if (destroy_handler_id_) {
        g_signal_handler_disconnect(window_, destroy_handler_id_);
    }
    if (size_handler_id_) {
        g_signal_handler_disconnect(container_, size_handler_id_);
    }

    if (browser_) {
        browser_->GetHost()->CloseBrowser(true);
        browser_ = nullptr;
    }
}

void WindowHost::EmbedBrowser(CefRefPtr<CefBrowser> browser) {
    browser_ = browser;

    auto host = browser->GetHost();
    browser_id_ = browser->GetIdentifier();

    GtkWidget* socket = gtk_socket_new();
    gtk_widget_set_vexpand(socket, TRUE);
    gtk_widget_set_hexpand(socket, TRUE);

    gtk_container_add(GTK_CONTAINER(container_), socket);
    gtk_widget_show(socket);

    gulong id = g_signal_connect(
        G_OBJECT(socket), "size-allocate",
        G_CALLBACK(+[](GtkWidget* widget, GdkRectangle* alloc, gpointer data) {
            auto* self = static_cast<WindowHost*>(data);
            if (self->browser_) {
                auto host = self->browser_->GetHost();
                if (host) {
                    CefRect bounds(0, 0, alloc->width, alloc->height);
                    host->WasResized();
                }
            }
        }), this);

    size_handler_id_ = id;

    Window socket_id = gtk_socket_get_id(GTK_SOCKET(socket));
    CefWindowInfo win_info;
    win_info.SetAsWindowless(socket_id);
}

void WindowHost::Close() {
    if (browser_) {
        browser_->GetHost()->CloseBrowser(true);
        browser_ = nullptr;
    }
}
