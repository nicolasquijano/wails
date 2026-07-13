#include "ipc_handler.h"

#include <algorithm>
#include <cerrno>
#include <cstdarg>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <functional>
#include <string>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <unistd.h>
#include <vector>

#include <gio/gunixsocketaddress.h>
#include <glib.h>
#include <json/json.h>
#include <mutex>

namespace {

constexpr uint8_t kFrameHeaderSize = 4;

std::string FormatError(const char* context, int err) {
    std::string result = context;
    result += ": ";
    result += strerror(err);
    return result;
}

bool SetCloseOnExec(int fd) {
    int flags = fcntl(fd, F_GETFD, 0);
    if (flags < 0) return false;
    return fcntl(fd, F_SETFD, flags | FD_CLOEXEC) >= 0;
}

}  // namespace

IpcHandlerRegistry& IpcHandlerRegistry::Instance() {
    static IpcHandlerRegistry instance;
    return instance;
}

void IpcHandlerRegistry::Register(IpcHandler* handler) {
    handlers_.push_back(handler);
}

void IpcHandlerRegistry::Unregister(IpcHandler* handler) {
    handlers_.erase(
        std::remove(handlers_.begin(), handlers_.end(), handler),
        handlers_.end());
}

void IpcHandlerRegistry::Dispatch(Envelope&& env) {
    for (auto* handler : handlers_) {
        handler->OnEnvelope(std::move(env));
    }
}

void IpcHandlerRegistry::NotifyConnected(int peer_pid) {
    for (auto* handler : handlers_) {
        handler->OnConnected(peer_pid);
    }
}

void IpcHandlerRegistry::NotifyDisconnected() {
    for (auto* handler : handlers_) {
        handler->OnDisconnected();
    }
}

bool SendFrame(int fd, std::string_view frame) {
    if (frame.size() > kMaxPayloadSize) {
        return false;
    }

    uint32_t len = static_cast<uint32_t>(frame.size());
    uint8_t header[kFrameHeaderSize];
    header[0] = (len >> 24) & 0xFF;
    header[1] = (len >> 16) & 0xFF;
    header[2] = (len >> 8) & 0xFF;
    header[3] = len & 0xFF;

    std::vector<uint8_t> msg;
    msg.reserve(sizeof(header) + frame.size());
    msg.insert(msg.end(), header, header + sizeof(header));
    msg.insert(msg.end(), frame.begin(), frame.end());

    ssize_t written = 0;
    while (written < static_cast<ssize_t>(msg.size())) {
        ssize_t n = write(fd, msg.data() + written, msg.size() - written);
        if (n < 0) {
            if (errno == EINTR) continue;
            return false;
        }
        if (n == 0) return false;
        written += n;
    }
    return true;
}

std::vector<uint8_t> RecvFrame(int fd, std::string* err) {
    uint8_t header[kFrameHeaderSize];
    ssize_t n = recv(fd, header, sizeof(header), MSG_WAITALL);
    if (n < 0) {
        *err = FormatError("recv header", errno);
        return {};
    }
    if (n == 0) {
        return {};
    }
    if (n != sizeof(header)) {
        *err = "incomplete header";
        return {};
    }

    uint32_t len = (static_cast<uint32_t>(header[0]) << 24) |
                   (static_cast<uint32_t>(header[1]) << 16) |
                   (static_cast<uint32_t>(header[2]) << 8) |
                   static_cast<uint32_t>(header[3]);

    if (len > kMaxPayloadSize) {
        *err = "payload too large: " + std::to_string(len);
        return {};
    }

    std::vector<uint8_t> payload(len);
    size_t received = 0;
    while (received < len) {
        n = recv(fd, payload.data() + received, len - received, MSG_WAITALL);
        if (n < 0) {
            if (errno == EINTR) continue;
            *err = FormatError("recv payload", errno);
            return {};
        }
        if (n == 0) {
            *err = "peer closed connection";
            return {};
        }
        received += n;
    }
    return payload;
}

UnixSocketServer::UnixSocketServer(const std::string& path, SocketListener* listener)
    : path_(path), listener_(listener) {}

UnixSocketServer::~UnixSocketServer() {
    Shutdown();
}

bool UnixSocketServer::Listen() {
    unlink(path_.c_str());

    server_fd_ = socket(AF_UNIX, SOCK_STREAM, 0);
    if (server_fd_ < 0) {
        return false;
    }

    SetCloseOnExec(server_fd_);

    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, path_.c_str(), sizeof(addr.sun_path) - 1);

    if (bind(server_fd_, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) < 0) {
        close(server_fd_);
        server_fd_ = -1;
        return false;
    }

    if (listen(server_fd_, 5) < 0) {
        close(server_fd_);
        server_fd_ = -1;
        unlink(path_.c_str());
        return false;
    }

    chmod(path_.c_str(), 0600);

    GIOChannel* channel = g_io_channel_unix_new(server_fd_);
    source_id_ = g_io_add_watch(channel, static_cast<GIOCondition>(G_IO_IN | G_IO_ERR | G_IO_HUP),
                                 OnIOChannel, this);
    g_io_channel_unref(channel);

    return true;
}

void UnixSocketServer::Shutdown() {
    if (source_id_) {
        g_source_remove(source_id_);
        source_id_ = 0;
    }
    if (server_fd_ >= 0) {
        close(server_fd_);
        server_fd_ = -1;
    }
    unlink(path_.c_str());
}

gboolean UnixSocketServer::OnIOChannel(GIOChannel* source, GIOCondition condition,
                                       gpointer data) {
    auto* server = static_cast<UnixSocketServer*>(data);

    if (condition & (G_IO_ERR | G_IO_HUP)) {
        return G_SOURCE_REMOVE;
    }

    int client_fd = accept(server->server_fd_, nullptr, nullptr);
    if (client_fd < 0) {
        return G_SOURCE_CONTINUE;
    }

    SetCloseOnExec(client_fd);

    if (!server->listener_) {
        close(client_fd);
        return G_SOURCE_CONTINUE;
    }

    std::string err;
    std::vector<uint8_t> frame = RecvFrame(client_fd, &err);
    if (!frame.empty()) {
        server->listener_->OnFrame(client_fd, std::move(frame));
    } else if (!err.empty()) {
        server->listener_->OnError(err);
    }

    return G_SOURCE_CONTINUE;
}

AuthenticatedListener::AuthenticatedListener(const std::string& expected_capability, uid_t expected_uid)
    : expected_capability_(expected_capability), expected_uid_(expected_uid) {}

void AuthenticatedListener::OnFrame(int client_fd, std::vector<uint8_t>&& frame) {
    ValidateAndDispatch(client_fd, std::move(frame));
}

void AuthenticatedListener::OnError(const std::string& msg) {
    if (next_) {
        next_->OnError(msg);
    }
}

bool AuthenticatedListener::ValidateAndDispatch(int client_fd, std::vector<uint8_t>&& frame) {
    Envelope env = ParseEnvelope(frame, nullptr);

    if (env.kind != EnvelopeKind::Hello) {
        if (next_) {
            next_->OnError("expected hello, got kind=" + std::to_string(static_cast<int>(env.kind)));
        }
        return false;
    }

    struct ucred cred;
    socklen_t cred_len = sizeof(cred);
    if (getsockopt(client_fd, SOL_SOCKET, SO_PEERCRED, &cred, &cred_len) < 0) {
        if (next_) {
            next_->OnError("SO_PEERCRED failed: " + std::string(strerror(errno)));
        }
        return false;
    }

    if (cred.uid != expected_uid_) {
        if (next_) {
            next_->OnError("uid mismatch: got " + std::to_string(cred.uid) +
                          ", expected " + std::to_string(expected_uid_));
        }
        return false;
    }

    if (!expected_capability_.empty() && env.capability != expected_capability_) {
        if (next_) {
            next_->OnError("capability mismatch");
        }
        return false;
    }

    Envelope ready;
    ready.version = 1;
    ready.kind = EnvelopeKind::Ready;
    ready.capability = expected_capability_;
    ready.payload = std::vector<uint8_t>(std::to_string(getpid()).begin(),
                                         std::to_string(getpid()).end());

    std::vector<uint8_t> resp = SerializeEnvelope(ready);
    SendFrame(client_fd, std::string(resp.begin(), resp.end()));

    if (next_) {
        next_->OnFrame(client_fd, std::move(frame));
    }

    return true;
}

Envelope ParseEnvelope(const std::vector<uint8_t>& data, std::string* err) {
    Envelope env;

    if (data.empty()) {
        if (err) *err = "empty data";
        return env;
    }

    Json::Value root;
    Json::String parse_err;
    if (!Json::Reader().parse(std::string(data.begin(), data.end()), root)) {
        if (err) *err = "JSON parse error: " + parse_err;
        return env;
    }

    env.version = root.get("v", 0).asInt();
    if (env.version != 1) {
        if (err) *err = "unsupported protocol version: " + std::to_string(env.version);
        return env;
    }

    env.kind = static_cast<EnvelopeKind>(root.get("kind", 0).asInt());
    env.capability = root.get("capability", "").asString();
    env.request_id = root.get("id", "").asString();
    env.browser_id = root.get("browserId", 0).asInt();
    env.frame_id = root.get("frameId", "").asString();
    env.window_id = root.get("windowId", 0).asInt();
    env.operation = root.get("operation", "").asString();
    env.deadline_unix_ms = root.get("deadlineUnixMs", 0).asInt64();
    env.ok = root.get("ok", true).asBool();
    env.error_code = root.get("error_code", "").asString();
    env.error_message = root.get("error_message", "").asString();
    env.reason = root.get("reason", "").asString();

    const Json::Value& payload = root["payload"];
    if (!payload.isNull()) {
        if (payload.isString()) {
            env.payload = std::vector<uint8_t>(payload.asString().begin(), payload.asString().end());
        }
    }

    return env;
}

std::vector<uint8_t> SerializeEnvelope(const Envelope& env) {
    Json::Value root;
    root["v"] = env.version;
    root["kind"] = static_cast<int>(env.kind);
    root["capability"] = env.capability;
    root["id"] = env.request_id;
    root["browserId"] = env.browser_id;
    root["frameId"] = env.frame_id;
    root["windowId"] = env.window_id;
    root["operation"] = env.operation;
    root["deadlineUnixMs"] = static_cast<Json::Int64>(env.deadline_unix_ms);
    root["ok"] = env.ok;
    if (!env.error_code.empty()) root["error_code"] = env.error_code;
    if (!env.error_message.empty()) root["error_message"] = env.error_message;
    if (!env.reason.empty()) root["reason"] = env.reason;

    if (!env.payload.empty()) {
        root["payload"] = Json::Value(std::string(env.payload.begin(), env.payload.end()));
    }

    std::string json = Json::FastWriter().write(root);
    return std::vector<uint8_t>(json.begin(), json.end());
}

RpcChannel& RpcChannel::Instance() {
    static RpcChannel instance;
    return instance;
}

void RpcChannel::RegisterBrowser(int browser_id, int client_fd) {
    std::lock_guard<std::mutex> lock(fd_mutex_);
    browser_to_fd_[browser_id] = client_fd;
}

void RpcChannel::UnregisterBrowser(int browser_id) {
    std::lock_guard<std::mutex> lock(fd_mutex_);
    browser_to_fd_.erase(browser_id);
}

int RpcChannel::GetClientFd(int browser_id) const {
    std::lock_guard<std::mutex> lock(fd_mutex_);
    auto it = browser_to_fd_.find(browser_id);
    if (it != browser_to_fd_.end()) {
        return it->second;
    }
    return -1;
}

void RpcChannel::SendResponse(const std::string& request_id,
                              bool ok,
                              const std::string& error_code,
                              const std::string& error_message,
                              const std::string& payload,
                              int browser_id,
                              const std::string& frame_id,
                              int window_id) {
    Envelope env;
    env.version = 1;
    env.kind = EnvelopeKind::Response;
    env.capability = capability_;
    env.request_id = request_id;
    env.browser_id = browser_id;
    env.frame_id = frame_id;
    env.window_id = window_id;
    env.ok = ok;
    env.error_code = error_code;
    env.error_message = error_message;
    if (!payload.empty()) {
        env.payload = std::vector<uint8_t>(payload.begin(), payload.end());
    }

    int fd = GetClientFd(browser_id);
    if (fd >= 0) {
        std::vector<uint8_t> data = SerializeEnvelope(env);
        std::string frame(data.begin(), data.end());
        SendFrame(fd, frame);
    }
}

void RpcChannel::SendEvent(const std::string& operation,
                          const std::string& payload,
                          int browser_id,
                          const std::string& frame_id,
                          int window_id) {
    Envelope env;
    env.version = 1;
    env.kind = EnvelopeKind::Event;
    env.capability = capability_;
    env.browser_id = browser_id;
    env.frame_id = frame_id;
    env.window_id = window_id;
    env.operation = operation;
    if (!payload.empty()) {
        env.payload = std::vector<uint8_t>(payload.begin(), payload.end());
    }

    if (browser_id > 0) {
        int fd = GetClientFd(browser_id);
        if (fd >= 0) {
            std::vector<uint8_t> data = SerializeEnvelope(env);
            std::string frame(data.begin(), data.end());
            SendFrame(fd, frame);
        }
    } else {
        std::lock_guard<std::mutex> lock(fd_mutex_);
        for (const auto& [bid, fd] : browser_to_fd_) {
            std::vector<uint8_t> data = SerializeEnvelope(env);
            std::string frame(data.begin(), data.end());
            SendFrame(fd, frame);
        }
    }
}
