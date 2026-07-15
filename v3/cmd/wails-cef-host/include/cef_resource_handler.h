#pragma once

#include <cef_resource_handler.h>
#include <cef_request.h>
#include <cef_response.h>
#include <cef_scheme.h>
#include <memory>
#include <string>
#include <vector>

class AssetRequestHandler;

class AssetResourceHandler : public CefResourceHandler {
public:
    AssetResourceHandler(AssetRequestHandler* parent, int browser_id, const std::string& url);
    ~AssetResourceHandler() override;

    bool ProcessRequest(CefRefPtr<CefRequest> request,
                       CefRefPtr<CefCallback> callback) override;
    void GetResponseHeaders(CefRefPtr<CefResponse> response,
                            int64_t& response_length,
                            CefString& redirectUrl) override;
    void Cancel() override;
    bool ReadResponse(void* data_out,
                     int bytes_to_read,
                     int& bytes_read,
                     CefRefPtr<CefCallback> callback) override;

    void SetResponseBody(const std::vector<uint8_t>& body);

private:
    AssetRequestHandler* parent_;
    int browser_id_;
    std::string url_;
    std::vector<uint8_t> response_body_;
    size_t offset_ = 0;
    bool headers_set_ = false;
    bool response_ready_ = false;

    CefRefPtr<CefRequest> request_;
    CefRefPtr<CefCallback> request_callback_;

    IMPLEMENT_REFCOUNTING(AssetResourceHandler);
    DISALLOW_COPY_AND_ASSIGN(AssetResourceHandler);
};

class AssetRequestHandler : public CefSchemeHandlerFactory {
public:
    explicit AssetRequestHandler(const std::string& socket_path,
                                 const std::string& capability,
                                 const std::string& assets_dir);
    ~AssetRequestHandler() override;

    CefRefPtr<CefResourceHandler> Create(
        CefRefPtr<CefBrowser> browser,
        CefRefPtr<CefFrame> frame,
        const CefString& scheme_name,
        CefRefPtr<CefRequest> request) override;

    void SendAssetRequest(const std::string& method,
                          const std::string& url,
                          const std::string& post_data,
                          int browser_id,
                          AssetResourceHandler* handler);

private:
    bool ConnectToGo();
    void DisconnectFromGo();
    bool SendToGo(const std::vector<uint8_t>& data);
    std::vector<uint8_t> RecvFromGo();

    std::string socket_path_;
    std::string capability_;
    std::string assets_dir_;
    int client_fd_ = -1;

    friend class AssetResourceHandler;

    IMPLEMENT_REFCOUNTING(AssetRequestHandler);
    DISALLOW_COPY_AND_ASSIGN(AssetRequestHandler);
};
