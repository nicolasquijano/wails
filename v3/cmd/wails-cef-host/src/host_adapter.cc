#include "host_adapter.h"
#include "ipc_handler.h"

#include <cstdio>
#include <cstdlib>
#include <json/json.h>
#include <gtk/gtk.h>

HostAdapter::HostAdapter() = default;

HostAdapter::~HostAdapter() {
    for (auto& [id, menu] : menus_) {
        if (menu) {
            gtk_widget_destroy(menu);
        }
    }
    menus_.clear();
}

void HostAdapter::OnRequest(const std::string& request_id,
                            const std::string& operation,
                            const std::string& payload,
                            int browser_id,
                            int window_id) {
    browser_id_ = browser_id;
    window_id_ = window_id;

    if (operation.rfind("host.window.", 0) == 0) {
        HandleWindowOperation(request_id, operation, payload);
    } else if (operation.rfind("host.dialog.", 0) == 0) {
        HandleDialogOperation(request_id, operation, payload);
    } else if (operation.rfind("host.clipboard.", 0) == 0) {
        HandleClipboardOperation(request_id, operation, payload);
    } else if (operation.rfind("host.menu.", 0) == 0) {
        HandleMenuOperation(request_id, operation, payload);
    } else {
        SendResponse(request_id, false, "unsupported_operation",
                     "unknown host operation: " + operation, "{}");
    }
}

void HostAdapter::HandleWindowOperation(const std::string& request_id,
                                       const std::string& operation,
                                       const std::string& payload) {
    std::string result;

    if (operation == "host.window.setTitle") {
        result = WindowSetTitle(payload);
    } else if (operation == "host.window.getSize") {
        result = WindowGetSize();
    } else if (operation == "host.window.setSize") {
        result = WindowSetSize(payload);
    } else if (operation == "host.window.getPosition") {
        result = WindowGetPosition();
    } else if (operation == "host.window.setPosition") {
        result = WindowSetPosition(payload);
    } else if (operation == "host.window.maximise") {
        result = WindowMaximize();
    } else if (operation == "host.window.unmaximise") {
        result = WindowUnmaximize();
    } else if (operation == "host.window.minimise") {
        result = WindowMinimize();
    } else if (operation == "host.window.restore") {
        result = WindowRestore();
    } else if (operation == "host.window.setAlwaysOnTop") {
        result = WindowSetAlwaysOnTop(payload);
    } else if (operation == "host.window.isMaximised") {
        result = WindowIsMaximised();
    } else if (operation == "host.window.isMinimised") {
        result = WindowIsMinimised();
    } else if (operation == "host.window.isFullscreen") {
        result = WindowIsFullscreen();
    } else if (operation == "host.window.setDecorations") {
        result = WindowSetDecorations(payload);
    } else if (operation == "host.window.setResizable") {
        result = WindowSetResizable(payload);
    } else if (operation == "host.window.center") {
        result = WindowCenter();
    } else {
        SendResponse(request_id, false, "unsupported_operation",
                     "unknown window operation: " + operation, "{}");
        return;
    }

    SendResponse(request_id, true, "", "", result);
}

void HostAdapter::HandleDialogOperation(const std::string& request_id,
                                       const std::string& operation,
                                       const std::string& payload) {
    std::string result;

    if (operation == "host.dialog.openFile") {
        result = DialogOpenFile(payload);
    } else if (operation == "host.dialog.saveFile") {
        result = DialogSaveFile(payload);
    } else if (operation == "host.dialog.message") {
        result = DialogMessage(payload);
    } else {
        SendResponse(request_id, false, "unsupported_operation",
                     "unknown dialog operation: " + operation, "{}");
        return;
    }

    SendResponse(request_id, true, "", "", result);
}

void HostAdapter::HandleClipboardOperation(const std::string& request_id,
                                          const std::string& operation,
                                          const std::string& payload) {
    std::string result;

    if (operation == "host.clipboard.readText") {
        result = ClipboardReadText();
    } else if (operation == "host.clipboard.writeText") {
        result = ClipboardWriteText(payload);
    } else {
        SendResponse(request_id, false, "unsupported_operation",
                     "unknown clipboard operation: " + operation, "{}");
        return;
    }

    SendResponse(request_id, true, "", "", result);
}

void HostAdapter::HandleMenuOperation(const std::string& request_id,
                                     const std::string& operation,
                                     const std::string& payload) {
    std::string result;

    if (operation == "host.menu.create") {
        result = MenuCreate(payload);
    } else if (operation == "host.menu.popup") {
        result = MenuPopup(payload);
    } else if (operation == "host.menu.destroy") {
        result = MenuDestroy(payload);
    } else {
        SendResponse(request_id, false, "unsupported_operation",
                     "unknown menu operation: " + operation, "{}");
        return;
    }

    SendResponse(request_id, true, "", "", result);
}

std::string HostAdapter::WindowSetTitle(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string title = root.get("title", "").asString();
    if (window_) {
        gtk_window_set_title(GTK_WINDOW(window_), title.c_str());
    }

    Json::Value result;
    result["title"] = title;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowGetSize() {
    Json::Value result;
    if (window_) {
        int width, height;
        gtk_window_get_size(GTK_WINDOW(window_), &width, &height);
        result["width"] = width;
        result["height"] = height;
    } else {
        result["width"] = 0;
        result["height"] = 0;
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowSetSize(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    int width = root.get("width", 800).asInt();
    int height = root.get("height", 600).asInt();
    bool center = root.get("center", false).asBool();

    if (window_) {
        GdkGeometry geometry;
        geometry.min_width = 1;
        geometry.min_height = 1;
        geometry.max_width = G_MAXINT;
        geometry.max_height = G_MAXINT;
        gtk_window_set_geometry_hints(GTK_WINDOW(window_), nullptr, &geometry,
                                      GdkWindowHints(GDK_HINT_MIN_SIZE | GDK_HINT_MAX_SIZE));
        gtk_window_resize(GTK_WINDOW(window_), width, height);
        if (center) {
            gtk_window_set_position(GTK_WINDOW(window_), GTK_WIN_POS_CENTER);
        }
    }

    Json::Value result;
    result["width"] = width;
    result["height"] = height;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowGetPosition() {
    Json::Value result;
    if (window_) {
        int x, y;
        gtk_window_get_position(GTK_WINDOW(window_), &x, &y);
        result["x"] = x;
        result["y"] = y;
    } else {
        result["x"] = 0;
        result["y"] = 0;
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowSetPosition(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    int x = root.get("x", 0).asInt();
    int y = root.get("y", 0).asInt();

    if (window_) {
        gtk_window_move(GTK_WINDOW(window_), x, y);
    }

    Json::Value result;
    result["x"] = x;
    result["y"] = y;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowMaximize() {
    if (window_) {
        gtk_window_maximize(GTK_WINDOW(window_));
    }
    return "{\"maximised\": true}";
}

std::string HostAdapter::WindowUnmaximize() {
    if (window_) {
        gtk_window_unmaximize(GTK_WINDOW(window_));
    }
    return "{\"maximised\": false}";
}

std::string HostAdapter::WindowMinimize() {
    if (window_) {
        gtk_window_iconify(GTK_WINDOW(window_));
    }
    return "{\"minimised\": true}";
}

std::string HostAdapter::WindowRestore() {
    if (window_) {
        gtk_window_deiconify(GTK_WINDOW(window_));
    }
    return "{\"minimised\": false}";
}

std::string HostAdapter::WindowSetAlwaysOnTop(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    bool on_top = root.get("onTop", false).asBool();

    if (window_) {
        gtk_window_set_keep_above(GTK_WINDOW(window_), on_top ? TRUE : FALSE);
    }

    Json::Value result;
    result["onTop"] = on_top;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowIsMaximised() {
    Json::Value result;
    if (window_) {
        GdkWindow* gwin = gtk_widget_get_window(GTK_WIDGET(window_));
        if (gwin) {
            result["maximised"] = gdk_window_get_state(gwin) & GDK_WINDOW_STATE_MAXIMIZED;
        } else {
            result["maximised"] = false;
        }
    } else {
        result["maximised"] = false;
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowIsMinimised() {
    Json::Value result;
    if (window_) {
        GdkWindow* gwin = gtk_widget_get_window(GTK_WIDGET(window_));
        if (gwin) {
            result["minimised"] = gdk_window_get_state(gwin) & GDK_WINDOW_STATE_ICONIFIED;
        } else {
            result["minimised"] = false;
        }
    } else {
        result["minimised"] = false;
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowIsFullscreen() {
    Json::Value result;
    if (window_) {
        GdkWindow* gwin = gtk_widget_get_window(GTK_WIDGET(window_));
        if (gwin) {
            result["fullscreen"] = gdk_window_get_state(gwin) & GDK_WINDOW_STATE_FULLSCREEN;
        } else {
            result["fullscreen"] = false;
        }
    } else {
        result["fullscreen"] = false;
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowSetDecorations(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    bool decorated = root.get("decorated", true).asBool();

    if (window_) {
        gtk_window_set_decorated(GTK_WINDOW(window_), decorated ? TRUE : FALSE);
    }

    Json::Value result;
    result["decorated"] = decorated;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowSetResizable(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    bool resizable = root.get("resizable", true).asBool();

    if (window_) {
        gtk_window_set_resizable(GTK_WINDOW(window_), resizable ? TRUE : FALSE);
    }

    Json::Value result;
    result["resizable"] = resizable;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::WindowCenter() {
    if (window_) {
        gtk_window_set_position(GTK_WINDOW(window_), GTK_WIN_POS_CENTER);
    }
    return "{\"centered\": true}";
}

std::string HostAdapter::DialogOpenFile(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string title = root.get("title", "Open File").asString();
    bool multiple = root.get("multiple", false).asBool();
    std::vector<std::string> filters;

    if (root.isMember("filters")) {
        for (const auto& f : root["filters"]) {
            std::string pattern = f.get("pattern", "*").asString();
            filters.push_back(pattern);
        }
    }

    GtkWidget* dialog = gtk_file_chooser_dialog_new(
        title.c_str(),
        window_ ? GTK_WINDOW(window_) : nullptr,
        GTK_FILE_CHOOSER_ACTION_OPEN,
        "_Cancel", GTK_RESPONSE_CANCEL,
        "_Open", GTK_RESPONSE_ACCEPT,
        nullptr);

    if (multiple) {
        gtk_file_chooser_set_select_multiple(GTK_FILE_CHOOSER(dialog), TRUE);
    }

    for (size_t i = 0; i < filters.size(); ++i) {
        GtkFileFilter* filter = gtk_file_filter_new();
        gtk_file_filter_add_pattern(filter, filters[i].c_str());
        std::string name = "Filter " + std::to_string(i + 1);
        gtk_file_filter_set_name(filter, name.c_str());
        gtk_file_chooser_add_filter(GTK_FILE_CHOOSER(dialog), filter);
    }

    Json::Value result;
    gint response = gtk_dialog_run(GTK_DIALOG(dialog));

    if (response == GTK_RESPONSE_ACCEPT) {
        GSList* filenames = gtk_file_chooser_get_filenames(GTK_FILE_CHOOSER(dialog));
        if (multiple) {
            result["files"] = Json::Value(Json::arrayValue);
            for (GSList* l = filenames; l; l = l->next) {
                result["files"].append(static_cast<char*>(l->data));
                g_free(l->data);
            }
        } else {
            result["file"] = static_cast<char*>(filenames->data);
            g_slist_free_full(filenames, g_free);
        }
        if (filenames && !multiple) {
            g_free(filenames->data);
        }
        g_slist_free(filenames);
    }

    gtk_widget_destroy(dialog);

    result["cancelled"] = (response != GTK_RESPONSE_ACCEPT);
    return Json::FastWriter().write(result);
}

std::string HostAdapter::DialogSaveFile(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string title = root.get("title", "Save File").asString();
    std::string default_name = root.get("defaultFilename", "").asString();

    GtkWidget* dialog = gtk_file_chooser_dialog_new(
        title.c_str(),
        window_ ? GTK_WINDOW(window_) : nullptr,
        GTK_FILE_CHOOSER_ACTION_SAVE,
        "_Cancel", GTK_RESPONSE_CANCEL,
        "_Save", GTK_RESPONSE_ACCEPT,
        nullptr);

    gtk_file_chooser_set_do_overwrite_confirmation(GTK_FILE_CHOOSER(dialog), TRUE);

    if (!default_name.empty()) {
        gtk_file_chooser_set_current_name(GTK_FILE_CHOOSER(dialog), default_name.c_str());
    }

    Json::Value result;
    gint response = gtk_dialog_run(GTK_DIALOG(dialog));

    if (response == GTK_RESPONSE_ACCEPT) {
        char* filename = gtk_file_chooser_get_filename(GTK_FILE_CHOOSER(dialog));
        result["file"] = filename;
        g_free(filename);
    }

    gtk_widget_destroy(dialog);

    result["cancelled"] = (response != GTK_RESPONSE_ACCEPT);
    return Json::FastWriter().write(result);
}

std::string HostAdapter::DialogMessage(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string title = root.get("title", "Message").asString();
    std::string message = root.get("message", "").asString();
    std::string type_str = root.get("type", "info").asString();

    GtkMessageType type = GTK_MESSAGE_INFO;
    if (type_str == "error") {
        type = GTK_MESSAGE_ERROR;
    } else if (type_str == "warning") {
        type = GTK_MESSAGE_WARNING;
    } else if (type_str == "question") {
        type = GTK_MESSAGE_QUESTION;
    }

    GtkWidget* dialog = gtk_message_dialog_new(
        window_ ? GTK_WINDOW(window_) : nullptr,
        GTK_DIALOG_DESTROY_WITH_PARENT,
        type,
        GTK_BUTTONS_OK,
        "%s",
        message.c_str());

    if (!title.empty()) {
        gtk_window_set_title(GTK_WINDOW(dialog), title.c_str());
    }

    gtk_dialog_run(GTK_DIALOG(dialog));
    gtk_widget_destroy(dialog);

    Json::Value result;
    result["dismissed"] = true;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::ClipboardReadText() {
    Json::Value result;
    GtkClipboard* clipboard = gtk_clipboard_get(GDK_SELECTION_CLIPBOARD);
    char* text = gtk_clipboard_wait_for_text(clipboard);
    if (text) {
        result["text"] = text;
        g_free(text);
    } else {
        result["text"] = "";
    }
    return Json::FastWriter().write(result);
}

std::string HostAdapter::ClipboardWriteText(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string text = root.get("text", "").asString();
    GtkClipboard* clipboard = gtk_clipboard_get(GDK_SELECTION_CLIPBOARD);
    gtk_clipboard_set_text(clipboard, text.c_str(), text.length());
    gtk_clipboard_store(clipboard);

    Json::Value result;
    result["written"] = true;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::MenuCreate(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string id = std::to_string(++menu_id_counter_);
    GtkMenuShell* menu = GTK_MENU_SHELL(gtk_menu_new());

    menus_[id] = GTK_WIDGET(menu);

    Json::Value result;
    result["id"] = id;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::MenuPopup(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string id = root.get("id", "").asString();
    int x = root.get("x", 0).asInt();
    int y = root.get("y", 0).asInt();

    auto it = menus_.find(id);
    if (it == menus_.end()) {
        return "{\"error\": \"menu not found\"}";
    }

    gtk_menu_popup_at_widget(GTK_MENU(it->second), GTK_WIDGET(window_), GDK_GRAVITY_SOUTH_WEST, GDK_GRAVITY_NORTH_WEST, nullptr);

    Json::Value result;
    result["shown"] = true;
    return Json::FastWriter().write(result);
}

std::string HostAdapter::MenuDestroy(const std::string& payload) {
    Json::Value root;
    Json::String err;
    if (!Json::Reader().parse(payload, root)) {
        return "{\"error\": \"invalid JSON\"}";
    }

    std::string id = root.get("id", "").asString();

    auto it = menus_.find(id);
    if (it != menus_.end()) {
        gtk_widget_destroy(it->second);
        menus_.erase(it);
    }

    Json::Value result;
    result["destroyed"] = true;
    return Json::FastWriter().write(result);
}

void HostAdapter::SendResponse(const std::string& request_id, bool ok,
                               const std::string& error_code,
                               const std::string& error_message,
                               const std::string& payload) {
    RpcChannel::Instance().SendResponse(
        request_id, ok, error_code, error_message, payload,
        browser_id_, "", window_id_);
}

void HostAdapter::SendEvent(const std::string& operation,
                            const std::string& payload,
                            int browser_id,
                            int window_id) {
    RpcChannel::Instance().SendEvent(operation, payload,
                                     browser_id > 0 ? browser_id : browser_id_,
                                     "", window_id > 0 ? window_id : window_id_);
}
