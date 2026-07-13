#pragma once

#include <gtk/gtk.h>
#include <include/cef_client.h>
#include <string>
#include <unordered_map>

class HostAdapter {
public:
    explicit HostAdapter();
    ~HostAdapter();

    void OnRequest(const std::string& request_id,
                   const std::string& operation,
                   const std::string& payload,
                   int browser_id,
                   int window_id);

    void SetBrowserId(int id) { browser_id_ = id; }
    void SetWindow(GtkApplicationWindow* window) { window_ = window; }

    void SendResponse(const std::string& request_id, bool ok,
                      const std::string& error_code,
                      const std::string& error_message,
                      const std::string& payload);

    void SendEvent(const std::string& operation, const std::string& payload,
                   int browser_id = 0, int window_id = 0);

private:
    void HandleWindowOperation(const std::string& request_id,
                               const std::string& operation,
                               const std::string& payload);
    void HandleDialogOperation(const std::string& request_id,
                               const std::string& operation,
                               const std::string& payload);
    void HandleClipboardOperation(const std::string& request_id,
                                  const std::string& operation,
                                  const std::string& payload);
    void HandleMenuOperation(const std::string& request_id,
                             const std::string& operation,
                             const std::string& payload);

    std::string WindowSetTitle(const std::string& payload);
    std::string WindowGetSize();
    std::string WindowSetSize(const std::string& payload);
    std::string WindowGetPosition();
    std::string WindowSetPosition(const std::string& payload);
    std::string WindowMaximize();
    std::string WindowUnmaximize();
    std::string WindowMinimize();
    std::string WindowRestore();
    std::string WindowSetAlwaysOnTop(const std::string& payload);
    std::string WindowIsMaximised();
    std::string WindowIsMinimised();
    std::string WindowIsFullscreen();
    std::string WindowSetDecorations(const std::string& payload);
    std::string WindowSetResizable(const std::string& payload);
    std::string WindowCenter();

    std::string DialogOpenFile(const std::string& payload);
    std::string DialogSaveFile(const std::string& payload);
    std::string DialogMessage(const std::string& payload);

    std::string ClipboardReadText();
    std::string ClipboardWriteText(const std::string& payload);

    std::string MenuCreate(const std::string& payload);
    std::string MenuPopup(const std::string& payload);
    std::string MenuDestroy(const std::string& payload);

    static void OnDialogResponse(GtkDialog* dialog, gint response_id, gpointer data);

    GtkApplicationWindow* window_ = nullptr;
    int browser_id_ = 0;
    int window_id_ = 0;
    std::unordered_map<std::string, GtkWidget*> menus_;
    int menu_id_counter_ = 0;
};
