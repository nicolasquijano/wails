#pragma once

#include <include/cef_base.h>
#include <include/cef_app.h>
#include <include/cef_client.h>
#include <include/cef_frame.h>
#include <include/cef_process_message.h>
#include <memory>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

constexpr std::string_view kProtocolVersion = "1";
constexpr size_t kMaxPayloadSize = 16 * 1024 * 1024;
constexpr size_t kMaxConcurrentRequests = 256;
constexpr int kSocketTimeoutMs = 10000;

enum class EnvelopeKind : uint8_t {
    Hello = 0,
    Ready = 1,
    Request = 2,
    Response = 3,
    Event = 4,
    Cancel = 5,
    Shutdown = 6,
};

struct Envelope {
    int version = 1;
    EnvelopeKind kind;
    std::string capability;
    std::string request_id;
    int browser_id = 0;
    std::string frame_id;
    int window_id = 0;
    std::string operation;
    int64_t deadline_unix_ms = 0;
    std::vector<uint8_t> payload;
    bool ok = true;
    std::string error_code;
    std::string error_message;
    std::string reason;
};

class IpcHandler {
public:
    virtual ~IpcHandler() = default;
    virtual void OnEnvelope(Envelope&& env) = 0;
    virtual void OnConnected(int peer_pid) = 0;
    virtual void OnDisconnected() = 0;
};

class IpcHandlerRegistry {
public:
    static IpcHandlerRegistry& Instance();

    void Register(IpcHandler* handler);
    void Unregister(IpcHandler* handler);
    void Dispatch(Envelope&& env);
    void NotifyConnected(int peer_pid);
    void NotifyDisconnected();

private:
    IpcHandlerRegistry() = default;
    std::vector<IpcHandler*> handlers_;
};

class SocketListener {
public:
    virtual ~SocketListener() = default;
    virtual void OnFrame(std::vector<uint8_t>&& frame) = 0;
    virtual void OnError(const std::string& msg) = 0;
};

bool SendFrame(int fd, std::string_view frame);
std::vector<uint8_t> RecvFrame(int fd, std::string* err);

class UnixSocketServer {
public:
    UnixSocketServer(const std::string& path, SocketListener* listener);
    ~UnixSocketServer();

    bool Listen();
    void Shutdown();

private:
    static gboolean OnIOChannel(GIOChannel* source, GIOCondition condition, gpointer data);

    std::string path_;
    SocketListener* listener_;
    int server_fd_ = -1;
    guint source_id_ = 0;
};

class UnixSocketClient {
public:
    explicit UnixSocketClient(SocketListener* listener);
    ~UnixSocketClient();

    bool Connect(const std::string& path);
    void Disconnect();
    bool Send(std::string_view data);
    bool Connected() const { return fd_ >= 0; }

private:
    static gboolean OnIOChannel(GIOChannel* source, GIOCondition condition, gpointer data);

    SocketListener* listener_;
    int fd_ = -1;
    guint source_id_ = 0;
};

Envelope ParseEnvelope(const std::vector<uint8_t>& data, std::string* err);
std::vector<uint8_t> SerializeEnvelope(const Envelope& env);