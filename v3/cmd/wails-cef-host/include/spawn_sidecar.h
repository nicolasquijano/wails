// spawn_sidecar.h — Spawn the Go sidecar (wails-go-runtime) and run the
// authenticated handshake over a Unix-domain socket.
//
// This is M2 + M3 of Decision C18. The host creates a private socket under
// $XDG_RUNTIME_DIR/wails/<uuid>/, generates a 32-byte capability token,
// spawns the sidecar via posix_spawn with private argv, validates the
// hello envelope (capability + SO_PEERCRED UID), and sends a ready reply.
// On shutdown it sends a shutdown envelope, then SIGTERM + SIGKILL.
//
// The header is C++20, stdlib only, no external deps beyond POSIX. It is
// used only by wails-cef-host::main().

#pragma once

#include <string>

namespace wails_cef {

struct SpawnResult {
    bool ok = false;
    std::string socket_path;        // Path of the Unix socket.
    std::string runtime_dir;        // $XDG_RUNTIME_DIR/wails/<uuid>/
    std::string capability;         // 32-byte base64url token.
    pid_t sidecar_pid = -1;         // PID of wails-go-runtime, or -1.
    int client_fd = -1;             // Connected FD on the host side, or -1.
    std::string error_message;      // Populated when ok == false.
};

// Locate the wails-go-runtime binary. Resolution order:
//   1. argv0 --cef-host-sidecar-path (if provided)
//   2. $WAILS_CEF_SIDECAR environment variable
//   3. The directory containing the host executable / "wails-go-runtime"
//   4. $CEF_DIR/wails-go-runtime
std::string FindSidecarBinary(const std::string& argv0_path);

// Generate a 32-byte cryptographically random capability token, base64url
// encoded (43 characters, no padding). Used to authenticate the sidecar.
std::string GenerateCapabilityToken();

// Create $XDG_RUNTIME_DIR/wails/<uuid>/ with mode 0700. Returns the full
// path of the runtime dir, or empty on failure. The UUID is generated
// from /dev/urandom.
std::string CreateRuntimeDir();

// Bind a Unix-domain socket at <runtime_dir>/host.sock with mode 0600.
// Returns the listening FD, or -1 on failure. The caller takes ownership
// and must close() it.
int BindSidecarSocket(const std::string& runtime_dir, std::string* socket_path);

// Spawn wails-go-runtime via posix_spawn (or fork+exec). Returns the child
// PID. The child inherits no FDs other than stdio; the socket FD is not
// passed to the child (the child dials back via the path argument).
// On failure returns -1 and sets *err.
pid_t SpawnSidecar(const std::string& binary_path,
                   const std::string& socket_path,
                   const std::string& capability,
                   const std::string& assets_dir,
                   std::string* err);

// Accept one hello from the sidecar, validate (UID matches host UID,
// capability matches expected), and send a ready envelope. Returns the
// connected client_fd (caller takes ownership) on success, or -1 on
// failure. On failure, the sidecar child is killed and reaped.
int CompleteHandshake(int listen_fd,
                      const std::string& expected_capability,
                      uid_t expected_uid,
                      pid_t sidecar_pid,
                      std::string* err);

// Send a shutdown envelope on the connected fd (best-effort, non-blocking).
void SendShutdownEnvelope(int client_fd, const std::string& capability,
                          const std::string& reason);

// Terminate the sidecar child: SIGTERM, wait up to 2 seconds, SIGKILL,
// reap. Safe to call with -1.
void TerminateSidecar(pid_t sidecar_pid);

// Run the full M2+M3 sequence end-to-end. On success, *result is populated
// with the socket path, capability, sidecar PID and connected client_fd.
// On failure, any allocated resources are cleaned up.
SpawnResult SpawnAndHandshake(const std::string& argv0_path,
                               const std::string& assets_dir);

}  // namespace wails_cef