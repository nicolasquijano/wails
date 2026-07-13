#include "ipc_handler.h"

#include <algorithm>
#include <cerrno>
#include <cstdint>
#include <cstdio>
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

namespace {

constexpr uint8_t kFrameHeaderSize = 4;

std::string FormatError(const std::string& context, int err) {
    return context + ": " + strerror(err);
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
    source_id_ = g_io_add_watch(channel, G_IO_IN | G_IO_ERR | G_IO_HUP,
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

    std::vector<uint8_t> frame = RecvFrame(client_fd, nullptr);
    if (!frame.empty()) {
        server->listener_->OnFrame(std::move(frame));
    }

    return G_SOURCE_CONTINUE;
}

Envelope ParseEnvelope(const std::vector<uint8_t>& data, std::string* err) {
    Envelope env;

    if (data.size() < 2) {
        *err = "envelope too short";
        return env;
    }

    env.version = data[0];
    if (env.version != 1) {
        *err = "unsupported protocol version: " + std::to_string(env.version);
        return env;
    }

    env.kind = static_cast<EnvelopeKind>(data[1]);

    size_t pos = 2;

    auto read_string = [&](const std::string& field_name) -> std::string {
        if (pos >= data.size()) return "";
        size_t start = pos;
        while (pos < data.size() && data[pos] != 0) pos++;
        std::string result(data.begin() + start, data.begin() + pos);
        if (pos < data.size()) pos++;
        return result;
    };

    auto read_int = [&]() -> int64_t {
        if (pos + 8 > data.size()) return 0;
        int64_t val = 0;
        for (int i = 0; i < 8; ++i) {
            val = (val << 8) | data[pos++];
        }
        return val;
    };

    env.capability = read_string("capability");
    env.request_id = read_string("request_id");

    if (env.kind == EnvelopeKind::Request || env.kind == EnvelopeKind::Response ||
        env.kind == EnvelopeKind::Event) {
        env.browser_id = static_cast<int>(read_int());
        env.frame_id = read_string("frame_id");
        env.window_id = static_cast<int>(read_int());
        env.operation = read_string("operation");
        env.deadline_unix_ms = read_int();

        if (pos < data.size()) {
            env.payload.assign(data.begin() + pos, data.end());
        }
    }

    if (env.kind == EnvelopeKind::Response) {
        if (pos < data.size()) {
            env.ok = (data[pos] != 0);
            pos++;
        }
        if (pos < data.size()) {
            size_t err_start = pos;
            while (pos < data.size() && data[pos] != 0) pos++;
            env.error_code.assign(data.begin() + err_start, data.begin() + pos);
            if (pos < data.size()) pos++;
        }
        if (pos < data.size()) {
            size_t msg_start = pos;
            while (pos < data.size() && data[pos] != 0) pos++;
            env.error_message.assign(data.begin() + msg_start, data.begin() + pos);
        }
    }

    if (env.kind == EnvelopeKind::Hello) {
        if (pos < data.size()) {
            env.payload.assign(data.begin() + pos, data.end());
        }
    }

    return env;
}

std::vector<uint8_t> SerializeEnvelope(const Envelope& env) {
    std::vector<uint8_t> out;

    out.push_back(static_cast<uint8_t>(env.version));
    out.push_back(static_cast<uint8_t>(env.kind));

    auto append_string = [&](const std::string& s) {
        out.insert(out.end(), s.begin(), s.end());
        out.push_back(0);
    };

    auto append_int64 = [&](int64_t v) {
        uint8_t buf[8];
        for (int i = 7; i >= 0; --i) {
            buf[i] = v & 0xFF;
            v >>= 8;
        }
        out.insert(out.end(), buf, buf + 8);
    };

    append_string(env.capability);
    append_string(env.request_id);

    if (env.kind == EnvelopeKind::Request || env.kind == EnvelopeKind::Response ||
        env.kind == EnvelopeKind::Event) {
        append_int64(env.browser_id);
        append_string(env.frame_id);
        append_int64(env.window_id);
        append_string(env.operation);
        append_int64(env.deadline_unix_ms);
    }

    if (env.kind == EnvelopeKind::Response) {
        out.push_back(env.ok ? 1 : 0);
        append_string(env.error_code);
        append_string(env.error_message);
    }

    if (!env.payload.empty()) {
        out.insert(out.end(), env.payload.begin(), env.payload.end());
    }

    return out;
}
