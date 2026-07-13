#include "cef_resource_handler.h"

#include <cstring>
#include <fcntl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <unistd.h>

#include <cef_app.h>
#include <cef_browser.h>
#include <cef_frame.h>
#include <cef_request.h>
#include <cef_response.h>

#include "ipc_handler.h"

namespace {

constexpr size_t kMaxPayloadSize = 16 * 1024 * 1024;

bool SetNonBlocking(int fd) {
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0) return false;
    return fcntl(fd, F_SETFL, flags | O_NONBLOCK) >= 0;
}

}  // namespace

AssetResourceHandler::AssetResourceHandler(AssetRequestHandler* parent,
                                          int browser_id,
                                          const std::string& url)
    : parent_(parent), browser_id_(browser_id), url_(url) {}

AssetResourceHandler::~AssetResourceHandler() = default;

bool AssetResourceHandler::ProcessRequest(CefRefPtr<CefRequest> request,
                                       CefRefPtr<CefCallback> callback) {
    request_ = request;
    request_callback_ = callback;

    CefString post_data;
    request->GetPostData(&post_data);

    std::string method = "GET";
    if (request->GetMethod() == "POST") {
        method = "POST";
    }

    std::string url = url_;
    if (url.empty()) {
        request->GetURL(url);
    }

    std::string post_str;
    if (!post_data.empty()) {
        post_str = post_data;
    }

    parent_->SendAssetRequest(method, url, post_str, browser_id_, this);
    return true;
}

void AssetResourceHandler::GetResponseHeaders(CefRefPtr<CefResponse> response,
                                          int64& response_length,
                                          CefString& redirectUrl) {
    if (!response_body_.empty()) {
        response->SetStatus(200);
        response->SetStatusText("OK");
        response->SetMimeType("text/html");
        response_length = response_body_.size();
    } else {
        response->SetStatus(404);
        response->SetStatusText("Not Found");
        response_length = 0;
    }
}

bool AssetResourceHandler::ReadResponse(void* data_out,
                                      int bytes_to_read,
                                      int& bytes_read,
                                      CefRefPtr<CefCallback> callback) {
    if (offset_ >= response_body_.size()) {
        bytes_read = 0;
        return false;
    }

    size_t available = response_body_.size() - offset_;
    size_t to_copy = std::min(static_cast<size_t>(bytes_to_read), available);

    memcpy(data_out, response_body_.data() + offset_, to_copy);
    offset_ += to_copy;
    bytes_read = to_copy;

    return to_copy > 0;
}

void AssetResourceHandler::Cancel() {
    request_callback_ = nullptr;
}

AssetRequestHandler::AssetRequestHandler(const std::string& socket_path,
                                       const std::string& capability)
    : socket_path_(socket_path), capability_(capability) {}

AssetRequestHandler::~AssetRequestHandler() {
    DisconnectFromGo();
}

CefRefPtr<CefResourceHandler> AssetRequestHandler::CreateResourceHandler(
    CefRefPtr<CefBrowser> browser,
    CefRefPtr<CefFrame> frame,
    CefRefPtr<CefRequest> request) {

    std::string url;
    request->GetURL(url);

    return new AssetResourceHandler(this, browser->GetIdentifier(), url);
}

CefRefPtr<CefSchemeHandlerFactory> AssetRequestHandler::GetSchemeHandlerFactory() {
    return this;
}

void AssetRequestHandler::SendAssetRequest(const std::string& method,
                                        const std::string& url,
                                        const std::string& post_data,
                                        int browser_id,
                                        CefRefPtr<CefResourceHandler> handler) {
    if (!ConnectToGo()) {
        return;
    }

    Envelope env;
    env.version = 1;
    env.kind = EnvelopeKind::Request;
    env.capability = capability_;
    env.request_id = "asset-" + std::to_string(browser_id) + "-" + url;
    env.browser_id = browser_id;
    env.frame_id = "0";
    env.window_id = 0;
    env.operation = "asset.request";
    env.deadline_unix_ms = 0;

    env.payload = std::vector<uint8_t>(post_data.begin(), post_data.end());

    std::vector<uint8_t> frame = SerializeEnvelope(env);
    if (!SendToGo(frame)) {
        DisconnectFromGo();
        return;
    }

    std::vector<uint8_t> resp = RecvFromGo();
    if (resp.empty()) {
        DisconnectFromGo();
        return;
    }

    Envelope reply = ParseEnvelope(resp, nullptr);
    if (reply.kind == EnvelopeKind::Response && reply.ok) {
        auto* asset_handler = static_cast<AssetResourceHandler*>(handler.get());
        if (!reply.payload.empty()) {
            asset_handler->response_body_ = std::move(reply.payload);
        }
    }

    request_callback_->Continue();
}

bool AssetRequestHandler::ConnectToGo() {
    if (client_fd_ >= 0) {
        return true;
    }

    client_fd_ = socket(AF_UNIX, SOCK_STREAM, 0);
    if (client_fd_ < 0) {
        return false;
    }

    SetNonBlocking(client_fd_);

    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, socket_path_.c_str(), sizeof(addr.sun_path) - 1);

    if (connect(client_fd_, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) < 0) {
        close(client_fd_);
        client_fd_ = -1;
        return false;
    }

    Envelope hello;
    hello.version = 1;
    hello.kind = EnvelopeKind::Hello;
    hello.capability = capability_;
    hello.payload = std::vector<uint8_t>();

    std::vector<uint8_t> hello_frame = SerializeEnvelope(hello);
    if (!SendToGo(hello_frame)) {
        close(client_fd_);
        client_fd_ = -1;
        return false;
    }

    std::vector<uint8_t> ready_frame = RecvFromGo();
    if (ready_frame.empty()) {
        close(client_fd_);
        client_fd_ = -1;
        return false;
    }

    Envelope ready = ParseEnvelope(ready_frame, nullptr);
    if (ready.kind != EnvelopeKind::Ready) {
        close(client_fd_);
        client_fd_ = -1;
        return false;
    }

    return true;
}

void AssetRequestHandler::DisconnectFromGo() {
    if (client_fd_ >= 0) {
        close(client_fd_);
        client_fd_ = -1;
    }
}

bool AssetRequestHandler::SendToGo(const std::vector<uint8_t>& data) {
    if (client_fd_ < 0 || data.size() > kMaxPayloadSize) {
        return false;
    }

    uint32_t len = static_cast<uint32_t>(data.size());
    uint8_t header[4];
    header[0] = (len >> 24) & 0xFF;
    header[1] = (len >> 16) & 0xFF;
    header[2] = (len >> 8) & 0xFF;
    header[3] = len & 0xFF;

    std::vector<uint8_t> msg;
    msg.reserve(sizeof(header) + data.size());
    msg.insert(msg.end(), header, header + sizeof(header));
    msg.insert(msg.end(), data.begin(), data.end());

    ssize_t written = 0;
    while (written < static_cast<ssize_t>(msg.size())) {
        ssize_t n = write(client_fd_, msg.data() + written, msg.size() - written);
        if (n < 0) {
            if (errno == EINTR) continue;
            return false;
        }
        if (n == 0) return false;
        written += n;
    }
    return true;
}

std::vector<uint8_t> AssetRequestHandler::RecvFromGo() {
    uint8_t header[4];
    ssize_t n = recv(client_fd_, header, sizeof(header), MSG_WAITALL);
    if (n != sizeof(header)) {
        return {};
    }

    uint32_t len = (static_cast<uint32_t>(header[0]) << 24) |
                   (static_cast<uint32_t>(header[1]) << 16) |
                   (static_cast<uint32_t>(header[2]) << 8) |
                   static_cast<uint32_t>(header[3]);

    if (len > kMaxPayloadSize) {
        return {};
    }

    std::vector<uint8_t> payload(len);
    size_t received = 0;
    while (received < len) {
        n = recv(client_fd_, payload.data() + received, len - received, MSG_WAITALL);
        if (n <= 0) {
            return {};
        }
        received += n;
    }

    return payload;
}
