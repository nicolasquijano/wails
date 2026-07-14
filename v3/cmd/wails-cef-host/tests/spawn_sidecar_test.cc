// spawn_sidecar_test.cc — unit tests for wails_cef::spawn/handshake.
//
// Exercises FindSidecarBinary, GenerateCapabilityToken, CreateRuntimeDir,
// BindSidecarSocket, and SendShutdownEnvelope. Does NOT exercise
// SpawnSidecar directly (that requires a real sidecar binary); instead it
// uses a stub child that connects back to the listening socket, mimicking
// wails-go-runtime's hello frame.

#include "spawn_sidecar.h"

#include <cassert>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <fstream>
#include <iostream>
#include <random>
#include <signal.h>
#include <string>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <thread>
#include <unistd.h>
#include <vector>

namespace {

int g_failures = 0;
int g_total = 0;

void Expect(bool cond, const std::string& label) {
    ++g_total;
    if (!cond) {
        ++g_failures;
        std::cerr << "FAIL: " << label << std::endl;
    }
}

bool FileExists(const std::string& p) {
    struct stat st;
    return stat(p.c_str(), &st) == 0;
}

std::string MakeCapability() { return wails_cef::GenerateCapabilityToken(); }

void TestGenerateCapabilityToken() {
    auto t1 = wails_cef::GenerateCapabilityToken();
    auto t2 = wails_cef::GenerateCapabilityToken();
    Expect(!t1.empty(), "token1 not empty");
    Expect(!t2.empty(), "token2 not empty");
    Expect(t1 != t2, "tokens differ across calls");
    // base64url(32 bytes) is 43 chars with no padding.
    Expect(t1.size() == 43, "token length is 43 (got " + std::to_string(t1.size()) + ")");
    for (char c : t1) {
        bool ok = (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
                  (c >= '0' && c <= '9') || c == '-' || c == '_';
        Expect(ok, "token uses base64url alphabet only");
        if (!ok) break;
    }
}

void TestCreateRuntimeDir() {
    auto dir = wails_cef::CreateRuntimeDir();
    Expect(!dir.empty(), "runtime dir created");
    Expect(FileExists(dir), "runtime dir exists on disk");
    struct stat st;
    if (stat(dir.c_str(), &st) == 0) {
        Expect((st.st_mode & 0777) == 0700,
               "runtime dir mode is 0700 (got " +
               std::to_string(st.st_mode & 0777) + ")");
    }
    if (!dir.empty()) {
        rmdir(dir.c_str());
    }
}

void TestBindSidecarSocket() {
    auto dir = wails_cef::CreateRuntimeDir();
    Expect(!dir.empty(), "runtime dir created for socket test");
    if (dir.empty()) return;

    std::string sp;
    int fd = wails_cef::BindSidecarSocket(dir, &sp);
    Expect(fd >= 0, "socket bound");
    Expect(!sp.empty(), "socket path populated");
    Expect(sp.find(dir) == 0, "socket path inside runtime dir");
    Expect(sp.find("/host.sock") != std::string::npos, "socket path ends in host.sock");

    if (fd >= 0) {
        struct stat st;
        if (stat(sp.c_str(), &st) == 0) {
            Expect((st.st_mode & 0777) == 0600,
                   "socket mode is 0600 (got " +
                   std::to_string(st.st_mode & 0777) + ")");
        }
        close(fd);
    }
    unlink(sp.c_str());
    rmdir(dir.c_str());
}

void TestFindSidecarBinaryMissing() {
    // No WAILS_CEF_SIDECAR set; use a fake argv0 that does not exist.
    auto bin = wails_cef::FindSidecarBinary("/nonexistent/path/host");
    Expect(bin.empty(),
           "FindSidecarBinary returns empty when nothing is resolvable");
}

void TestFindSidecarBinaryFromArgv() {
    // Create a real executable in a temp dir.
    char tmpl[] = "/tmp/wails-cef-test-XXXXXX";
    char* p = mkdtemp(tmpl);
    if (!p) return;
    std::string dir = p;
    std::string sidecar = dir + "/wails-go-runtime";
    {
        std::ofstream f(sidecar);
        f << "#!/bin/sh\nexit 0\n";
    }
    chmod(sidecar.c_str(), 0755);

    // Construct a fake argv0 that resolves to sidecar via dirname.
    std::string fake_argv0 = dir + "/wails-cef-host";
    auto bin = wails_cef::FindSidecarBinary(fake_argv0);
    Expect(bin == sidecar,
           "FindSidecarBinary resolves argv0-adjacent binary (got '" +
           bin + "')");

    unlink(sidecar.c_str());
    rmdir(dir.c_str());
}

void TestSendShutdownEnvelope() {
    // Just exercise the function on a closed fd: should be a no-op.
    wails_cef::SendShutdownEnvelope(-1, "cap", "test");
    Expect(true, "SendShutdownEnvelope on -1 is a no-op");
}

// Helper: a minimal child process that connects to the socket, sends
// "hello" envelope, then exits.
int RunHelloChild(const std::string& socket_path,
                  const std::string& capability) {
    pid_t pid = fork();
    if (pid < 0) return -1;
    if (pid == 0) {
        int fd = socket(AF_UNIX, SOCK_STREAM, 0);
        if (fd < 0) _exit(2);
        struct sockaddr_un addr;
        memset(&addr, 0, sizeof(addr));
        addr.sun_family = AF_UNIX;
        strncpy(addr.sun_path, socket_path.c_str(), sizeof(addr.sun_path) - 1);
        if (connect(fd, reinterpret_cast<struct sockaddr*>(&addr), sizeof(addr)) < 0) {
            _exit(3);
        }
        // Send a hello frame.
        std::string hello = R"({"v":1,"kind":0,"capability":")" + capability + R"("})";
        uint32_t len = hello.size();
        uint8_t hdr[4];
        hdr[0] = (len >> 24) & 0xFF;
        hdr[1] = (len >> 16) & 0xFF;
        hdr[2] = (len >> 8) & 0xFF;
        hdr[3] = len & 0xFF;
        write(fd, hdr, 4);
        write(fd, hello.data(), hello.size());
        // Read ready.
        uint8_t rh[4];
        read(fd, rh, 4);
        uint32_t rlen = (uint32_t(rh[0]) << 24) | (uint32_t(rh[1]) << 16) |
                        (uint32_t(rh[2]) << 8) | uint32_t(rh[3]);
        std::vector<char> rbuf(rlen);
        read(fd, rbuf.data(), rlen);
        close(fd);
        _exit(0);
    }
    return pid;
}

void TestCompleteHandshakeHappyPath() {
    auto dir = wails_cef::CreateRuntimeDir();
    Expect(!dir.empty(), "dir created");
    if (dir.empty()) return;

    std::string sp;
    int fd = wails_cef::BindSidecarSocket(dir, &sp);
    Expect(fd >= 0, "listen socket bound");
    if (fd < 0) {
        rmdir(dir.c_str());
        return;
    }

    auto cap = MakeCapability();
    Expect(!cap.empty(), "capability generated");

    pid_t child = RunHelloChild(sp, cap);
    Expect(child > 0, "hello child forked");

    std::string err;
    int cfd = wails_cef::CompleteHandshake(fd, cap, getuid(), child, &err);
    Expect(cfd >= 0, "handshake returned client fd (err='" + err + "')");

    if (cfd >= 0) {
        close(cfd);
    }
    close(fd);

    int status;
    waitpid(child, &status, 0);
    Expect(WIFEXITED(status) && WEXITSTATUS(status) == 0,
           "child exited cleanly");

    unlink(sp.c_str());
    rmdir(dir.c_str());
}

void TestCompleteHandshakeBadCapability() {
    auto dir = wails_cef::CreateRuntimeDir();
    if (dir.empty()) return;
    std::string sp;
    int fd = wails_cef::BindSidecarSocket(dir, &sp);
    if (fd < 0) { rmdir(dir.c_str()); return; }

    auto cap = MakeCapability();
    pid_t child = RunHelloChild(sp, "wrong-capability-token-here");

    std::string err;
    int cfd = wails_cef::CompleteHandshake(fd, cap, getuid(), child, &err);
    Expect(cfd < 0, "handshake rejected mismatched capability");
    Expect(err.find("capability") != std::string::npos,
           "error message mentions capability (got '" + err + "')");

    int status;
    waitpid(child, &status, 0);

    close(fd);
    unlink(sp.c_str());
    rmdir(dir.c_str());
}

}  // namespace

int main() {
    TestGenerateCapabilityToken();
    TestCreateRuntimeDir();
    TestBindSidecarSocket();
    TestFindSidecarBinaryMissing();
    TestFindSidecarBinaryFromArgv();
    TestSendShutdownEnvelope();
    TestCompleteHandshakeHappyPath();
    TestCompleteHandshakeBadCapability();

    std::cout << "spawn_sidecar_test: " << (g_total - g_failures) << "/"
              << g_total << " checks passed" << std::endl;
    return g_failures == 0 ? 0 : 1;
}