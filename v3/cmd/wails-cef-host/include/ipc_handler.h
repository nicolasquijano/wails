#pragma once

#include <include/cef_base.h>
#include <include/cef_app.h>
#include <include/cef_client.h>
#include <include/cef_frame.h>
#include <include/cef_process_message.h>
#include <glib.h>
#include <gio/gio.h>
#include <memory>
#include <mutex>
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
    virtual void OnFrame(int client_fd, std::vector<uint8_t>&& frame) = 0;
    virtual void OnError(const std::string& msg) = 0;
};

class AuthenticatedListener : public SocketListener {
public:
    AuthenticatedListener(const std::string& expected_capability, uid_t expected_uid);
    void OnFrame(int client_fd, std::vector<uint8_t>&& frame) override;
    void OnError(const std::string& msg) override;
    void SetNext(SocketListener* next) { next_ = next; }

private:
    bool ValidateAndDispatch(int client_fd, std::vector<uint8_t>&& frame);

    std::string expected_capability_;
    uid_t expected_uid_;
    SocketListener* next_ = nullptr;
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

class RpcChannel {
public:
    static RpcChannel& Instance();

    void RegisterBrowser(int browser_id, int client_fd);
    void UnregisterBrowser(int browser_id);
    int GetClientFd(int browser_id) const;

    void SendResponse(const std::string& request_id,
                     bool ok,
                     const std::string& error_code,
                     const std::string& error_message,
                     const std::string& payload,
                     int browser_id,
                     const std::string& frame_id,
                     int window_id);

    void SendEvent(const std::string& operation,
                   const std::string& payload,
                   int browser_id = 0,
                   const std::string& frame_id = "",
                   int window_id = 0);

    void SetCapability(const std::string& cap) { capability_ = cap; }

private:
    RpcChannel() = default;
    std::unordered_map<int, int> browser_to_fd_;
    mutable std::mutex fd_mutex_;
    std::string capability_;
};