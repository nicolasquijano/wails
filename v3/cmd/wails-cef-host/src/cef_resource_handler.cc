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

#include <fstream>
#include <iostream>

#include "ipc_handler.h"
#include <json/json.h>

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

    std::string method = request->GetMethod().ToString();
    std::string req_url = request->GetURL().ToString();

    std::string post_str;
    CefRefPtr<CefPostData> post_data = request->GetPostData();
    if (post_data) {
        CefPostData::ElementVector elements;
        post_data->GetElements(elements);
        if (!elements.empty()) {
            for (auto& element : elements) {
                if (element->GetType() == PDE_TYPE_BYTES) {
                    size_t size = element->GetBytesCount();
                    if (size > 0) {
                        std::vector<char> buffer(size);
                        size_t bytes_read = element->GetBytes(size, buffer.data());
                        if (bytes_read > 0) {
                            post_str.append(buffer.data(), bytes_read);
                        }
                    }
                }
            }
        }
    }

    parent_->SendAssetRequest(method, req_url, post_str, browser_id_, this);
    return true;
}

void AssetResourceHandler::GetResponseHeaders(CefRefPtr<CefResponse> response,
                                              int64_t& response_length,
                                              CefString& redirectUrl) {
    if (response_ready_ && !response_body_.empty()) {
        response->SetStatus(200);
        response->SetStatusText("OK");
        // Derive MIME type from file extension
        std::string mime = "text/html";
        size_t dot = url_.rfind('.');
        if (dot != std::string::npos) {
            std::string ext = url_.substr(dot);
            if (ext == ".js") mime = "application/javascript";
            else if (ext == ".css") mime = "text/css";
            else if (ext == ".png") mime = "image/png";
            else if (ext == ".svg") mime = "image/svg+xml";
            else if (ext == ".ico") mime = "image/x-icon";
            else if (ext == ".woff2") mime = "font/woff2";
            else if (ext == ".woff") mime = "font/woff";
            else if (ext == ".ttf") mime = "font/ttf";
            else if (ext == ".json") mime = "application/json";
        }
        response->SetMimeType(mime);
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

void AssetResourceHandler::SetResponseBody(const std::vector<uint8_t>& body) {
    response_body_ = body;
    offset_ = 0;
    response_ready_ = true;
    if (request_callback_) {
        request_callback_->Continue();
        request_callback_ = nullptr;
    }
}

AssetRequestHandler::AssetRequestHandler(const std::string& socket_path,
                                         const std::string& capability,
                                         const std::string& assets_dir)
    : socket_path_(socket_path), capability_(capability), assets_dir_(assets_dir) {}

AssetRequestHandler::~AssetRequestHandler() {
    DisconnectFromGo();
}

CefRefPtr<CefResourceHandler> AssetRequestHandler::Create(
    CefRefPtr<CefBrowser> browser,
    CefRefPtr<CefFrame> frame,
    const CefString& scheme_name,
    CefRefPtr<CefRequest> request) {
    std::string url = request->GetURL().ToString();
    return new AssetResourceHandler(this, browser->GetIdentifier(), url);
}

void AssetRequestHandler::SendAssetRequest(const std::string& method,
                                          const std::string& url,
                                          const std::string& post_data,
                                          int browser_id,
                                          AssetResourceHandler* handler) {
    std::cerr << "wails-cef-host: AssetRequest url=" << url
              << " assets_dir=" << assets_dir_ << std::endl;

    // Serve assets directly from the filesystem instead of going through
    // the IPC socket (which is closed after the handshake — see
    // spawn_sidecar.cc:close(listen_fd)).
    //
    // Parse the wails:// URL path and join with assets_dir.
    // URL format: wails://localhost/<path>
    std::string path = url;
    std::string prefix = "wails://localhost";
    if (path.compare(0, prefix.size(), prefix) == 0) {
        path = path.substr(prefix.size());
    } else {
        // Try stripping scheme+host via ://
        auto colon_slash = path.find("://");
        if (colon_slash != std::string::npos) {
            auto host_start = colon_slash + 3;
            auto path_start = path.find('/', host_start);
            if (path_start != std::string::npos) {
                path = path.substr(path_start);
            } else {
                path = "/";
            }
        }
    }
    if (path.empty() || path == "/") {
        path = "/index.html";
    }

    std::string file_path = assets_dir_ + path;

    std::ifstream file(file_path, std::ios::binary | std::ios::ate);
    if (!file) {
        std::cerr << "wails-cef-host: file not found: " << file_path << std::endl;
        // SPA fallback: serve index.html
        std::ifstream fallback(assets_dir_ + "/index.html",
                               std::ios::binary | std::ios::ate);
        if (!fallback) {
            std::cerr << "wails-cef-host: SPA fallback also not found: "
                      << assets_dir_ << "/index.html" << std::endl;
            handler->SetResponseBody({});
            return;
        }
        size_t size = fallback.tellg();
        fallback.seekg(0);
        std::vector<uint8_t> data(size);
        fallback.read(reinterpret_cast<char*>(data.data()), size);
        std::cerr << "wails-cef-host: served SPA fallback (" << size
                  << " bytes)" << std::endl;
        handler->SetResponseBody(data);
        return;
    }

    size_t size = file.tellg();
    file.seekg(0);
    std::vector<uint8_t> data(size);
    file.read(reinterpret_cast<char*>(data.data()), size);
    std::cerr << "wails-cef-host: served " << file_path << " (" << size
              << " bytes)" << std::endl;
    handler->SetResponseBody(data);
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
    if (client_fd_ < 0 || data.size() > ::kMaxPayloadSize) {
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

    if (len > ::kMaxPayloadSize) {
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
