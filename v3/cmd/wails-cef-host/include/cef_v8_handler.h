#pragma once

#include <include/cef_v8.h>
#include <include/cef_client.h>
#include <memory>
#include <string>
#include <unordered_map>

class HostAdapter;

class V8Handler : public CefV8Handler {
public:
    V8Handler(const std::string& socket_path, const std::string& capability);
    ~V8Handler() override;

    bool Execute(const CefString& name,
                 CefRefPtr<CefV8Value> object,
                 const CefV8ValueList& arguments,
                 CefRefPtr<CefV8Value>& retval,
                 CefString& exception) override;

    void SetBrowser(int browser_id);
    void SetWindowId(int window_id) { window_id_ = window_id; }
    void SetHostAdapter(HostAdapter* adapter) { host_adapter_ = adapter; }

private:
    std::string socket_path_;
    std::string capability_;
    int browser_id_ = 0;
    int window_id_ = 0;
    int client_fd_ = -1;
    HostAdapter* host_adapter_ = nullptr;

    bool ConnectToGo();
    void DisconnectFromGo();
    bool SendToGo(const std::vector<uint8_t>& data);
    std::vector<uint8_t> RecvFromGo();

    struct PendingCall {
        CefRefPtr<CefV8Value> callback;
        CefRefPtr<CefV8Context> context;
    };
    std::unordered_map<std::string, PendingCall> pending_calls_;

    CefRefPtr<CefV8Value> CallGo(const std::string& method,
                                  const std::string& payload,
                                  CefRefPtr<CefV8Context> context,
                                  CefRefPtr<CefV8Value> callback);

    IMPLEMENT_REFCOUNTING(V8Handler);
    DISALLOW_COPY_AND_ASSIGN(V8Handler);
};
