#pragma once

#include <include/cef_client.h>

class HostClient : public CefClient {
public:
    HostClient() = default;

    CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return nullptr; }
    CefRefPtr<CefLoadHandler> GetLoadHandler() override { return nullptr; }
    CefRefPtr<CefFocusHandler> GetFocusHandler() override { return nullptr; }
    CefRefPtr<CefContextMenuHandler> GetContextMenuHandler() override { return nullptr; }
    CefRefPtr<CefDialogHandler> GetDialogHandler() override { return nullptr; }
    CefRefPtr<CefKeyboardHandler> GetKeyboardHandler() override { return nullptr; }
    CefRefPtr<CefDisplayHandler> GetDisplayHandler() override { return nullptr; }
    CefRefPtr<CefDragHandler> GetDragHandler() override { return nullptr; }
    CefRefPtr<CefFindHandler> GetFindHandler() override { return nullptr; }
    CefRefPtr<CefFrameHandler> GetFrameHandler() override { return nullptr; }
    CefRefPtr<CefPermissionHandler> GetPermissionHandler() override { return nullptr; }
    CefRefPtr<CefJSDialogHandler> GetJSDialogHandler() override { return nullptr; }
    CefRefPtr<CefPrintHandler> GetPrintHandler() override { return nullptr; }
    CefRefPtr<CefRenderHandler> GetRenderHandler() override { return nullptr; }
    CefRefPtr<CefRequestHandler> GetRequestHandler() override { return nullptr; }
    CefRefPtr<CefAudioHandler> GetAudioHandler() override { return nullptr; }
    CefRefPtr<CefCommandHandler> GetCommandHandler() override { return nullptr; }
    CefRefPtr<CefDownloadHandler> GetDownloadHandler() override { return nullptr; }

    IMPLEMENT_REFCOUNTING(HostClient);
    DISALLOW_COPY_AND_ASSIGN(HostClient);
};
