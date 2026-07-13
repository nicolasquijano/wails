#include "cef_v8_handler.h"
#include "host_adapter.h"

#include <cstring>
#include <fcntl.h>
#include <random>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <unistd.h>

#include <cef_app.h>
#include <cef_browser.h>
#include <cef_frame.h>
#include <cef_task.h>

#include <json/json.h>

#include "ipc_handler.h"

namespace {

constexpr size_t kMaxPayloadSize = 16 * 1024 * 1024;

std::string GenerateRequestId() {
    static std::random_device rd;
    static std::mt19937 gen(rd());
    static std::uniform_int_distribution<> dis(0, 255);

    std::string id;
    id.reserve(32);
    for (int i = 0; i < 16; ++i) {
        static const char hex[] = "0123456789abcdef";
        id += hex[dis(gen) % 16];
    }
    return id;
}

bool SetNonBlocking(int fd) {
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0) return false;
    return fcntl(fd, F_SETFL, flags | O_NONBLOCK) >= 0;
}

}  // namespace

V8Handler::V8Handler(const std::string& socket_path, const std::string& capability)
    : socket_path_(socket_path), capability_(capability) {}

V8Handler::~V8Handler() {
    DisconnectFromGo();
}

void V8Handler::SetBrowser(int browser_id) {
    browser_id_ = browser_id;
}

bool V8Handler::Execute(const CefString& name,
                         CefRefPtr<CefV8Value> object,
                         const CefV8ValueList& arguments,
                         CefRefPtr<CefV8Value>& retval,
                         CefString& exception) {

    if (name == "wails_invoke") {
        if (arguments.size() < 1) {
            exception = "wails_invoke requires at least 1 argument";
            return true;
        }

        CefRefPtr<CefV8Context> context = CefV8Context::GetCurrentContext();
        CefRefPtr<CefV8Value> payload_arg = arguments[0];
        std::string payload;

        if (payload_arg->IsString()) {
            payload = payload_arg->GetStringValue();
        } else if (payload_arg->IsObject()) {
            payload = "{\"type\":\"call\",\"data\":null}";
        }

        retval = CallGo("runtime.call", payload, context, nullptr);
        if (!retval) {
            exception = "CallGo failed";
        }
        return true;
    }

    if (name == "wails_invokeAsync") {
        if (arguments.size() < 2) {
            exception = "wails_invokeAsync requires at least 2 arguments";
            return true;
        }

        CefRefPtr<CefV8Context> context = CefV8Context::GetCurrentContext();
        CefRefPtr<CefV8Value> call_id_arg = arguments[0];
        CefRefPtr<CefV8Value> payload_arg = arguments[1];

        std::string call_id;
        if (call_id_arg->IsString()) {
            call_id = call_id_arg->GetStringValue();
        }

        std::string payload;
        if (payload_arg->IsString()) {
            payload = payload_arg->GetStringValue();
        }

        CefRefPtr<CefV8Value> callback;
        if (arguments.size() > 2 && arguments[2]->IsFunction()) {
            callback = arguments[2];
        }

        retval = CallGo("runtime.call", payload, context, callback);
        return true;
    }

    if (name == "wails_callback") {
        if (arguments.size() < 3) {
            exception = "wails_callback requires 3 arguments";
            return true;
        }
        return true;
    }

    if (name == "wails_log") {
        if (arguments.size() >= 1) {
            CefRefPtr<CefV8Value> msg = arguments[0];
            if (msg->IsString()) {
                CefRefPtr<CefBrowser> browser = CefV8Context::GetCurrentContext()->GetBrowser();
                if (browser) {
                    browser->GetMainFrame()->ExecuteJavaScript(
                        "console.log('[Go]', " + msg->GetStringValue() + ")",
                        "wails://native", 1);
                }
            }
        }
        return true;
    }

    exception = "Unknown function: " + name;
    return true;
}

CefRefPtr<CefV8Value> V8Handler::CallGo(const std::string& method,
                                         const std::string& payload,
                                         CefRefPtr<CefV8Context> context,
                                         CefRefPtr<CefV8Value> callback) {
    Json::Value root;
    Json::String parse_err;
    if (!Json::parse(payload, &root, &parse_err)) {
        return nullptr;
    }

    std::string operation;
    if (root.isMember("method")) {
        operation = root["method"].asString();
    }

    if (operation.rfind("host.", 0) == 0 && host_adapter_) {
        std::string request_id = GenerateRequestId();
        std::string operation_payload = "{}";
        if (root.isMember("args")) {
            operation_payload = Json::unparse(root["args"]);
        }
        host_adapter_->OnRequest(
            request_id, operation, operation_payload,
            browser_id_, window_id_);
        return CefV8Value::CreateString("{\"result\":\"ok\"}");
    }

    if (!ConnectToGo()) {
        return nullptr;
    }

    std::string request_id = GenerateRequestId();

    Envelope env;
    env.version = 1;
    env.kind = EnvelopeKind::Request;
    env.capability = capability_;
    env.request_id = request_id;
    env.browser_id = browser_id_;
    env.frame_id = "0";
    env.window_id = 0;
    env.operation = method;
    env.deadline_unix_ms = 30000;
    env.payload = std::vector<uint8_t>(payload.begin(), payload.end());

    std::vector<uint8_t> frame = SerializeEnvelope(env);
    if (!SendToGo(frame)) {
        DisconnectFromGo();
        return nullptr;
    }

    std::vector<uint8_t> resp = RecvFromGo();
    if (resp.empty()) {
        DisconnectFromGo();
        return nullptr;
    }

    Envelope reply = ParseEnvelope(resp, nullptr);
    CefRefPtr<CefV8Value> result_val;
    if (reply.kind == EnvelopeKind::Response && reply.ok) {
        std::string result_str(reply.payload.begin(), reply.payload.end());
        result_val = CefV8Value::CreateString(result_str);
    }

    return result_val;
}

bool V8Handler::ConnectToGo() {
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

void V8Handler::DisconnectFromGo() {
    if (client_fd_ >= 0) {
        close(client_fd_);
        client_fd_ = -1;
    }
}

bool V8Handler::SendToGo(const std::vector<uint8_t>& data) {
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

std::vector<uint8_t> V8Handler::RecvFromGo() {
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

bool V8Extension::GetFunction(const CefString& name,
                              CefRefPtr<CefV8Handler>& handler) {
    if (!handler_) {
        return false;
    }
    handler = handler_;
    return true;
}
