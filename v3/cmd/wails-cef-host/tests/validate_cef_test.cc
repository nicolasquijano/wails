// validate_cef_test.cc — unit tests for wails_cef::ValidateCefDistribution.
//
// Builds as a standalone executable (no CEF/GTK dependency) so it can run
// without a CEF SDK. Each test case constructs a temporary directory tree
// that simulates the layout required by Decision C16 and asserts the
// expected ValidationResult fields.

#include "validate_cef.h"

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>
#include <vector>

namespace fs = std::filesystem;

namespace {

int g_failures = 0;
int g_total = 0;

#define EXPECT(cond)                                                          \
    do {                                                                      \
        ++g_total;                                                            \
        if (!(cond)) {                                                        \
            ++g_failures;                                                     \
            std::cerr << "FAIL " << __FILE__ << ":" << __LINE__               \
                      << ": " << #cond << std::endl;                          \
        }                                                                     \
    } while (0)

void WriteFile(const std::string& path, const std::string& content = "") {
    fs::create_directories(fs::path(path).parent_path());
    std::ofstream f(path, std::ios::binary);
    f << content;
}

struct TempDir {
    std::string path;
    TempDir() {
        char tmpl[] = "/tmp/wails-cef-validate-XXXXXX";
        char* p = mkdtemp(tmpl);
        if (!p) {
            std::cerr << "mkdtemp failed" << std::endl;
            std::abort();
        }
        path = p;
    }
    ~TempDir() {
        std::error_code ec;
        fs::remove_all(path, ec);
    }
    std::string Join(const std::string& rel) const {
        return path + "/" + rel;
    }
    void TouchValidDistribution() {
        WriteFile(Join("libcef.so"), "fake");
        WriteFile(Join("icudtl.dat"), "fake");
        WriteFile(Join("Resources/v8_context_snapshot.bin"), "fake");
        WriteFile(Join("Resources/chrome_100_percent.pak"), "fake");
        WriteFile(Join("Resources/chrome_200_percent.pak"), "fake");
        WriteFile(Join("Resources/resources.pak"), "fake");
    }
};

void TestEmptyPath() {
    auto r = wails_cef::ValidateCefDistribution("");
    EXPECT(!r.ok);
    EXPECT(r.error_code == "empty_path");
}

void TestNonexistentPath() {
    auto r = wails_cef::ValidateCefDistribution("/tmp/this-path-must-not-exist-xyzzy");
    EXPECT(!r.ok);
    EXPECT(r.error_code == "not_a_directory");
}

void TestMissingLibcef() {
    TempDir td;
    WriteFile(td.Join("icudtl.dat"), "fake");
    auto r = wails_cef::ValidateCefDistribution(td.path);
    EXPECT(!r.ok);
    EXPECT(r.error_code == "missing_libcef");
}

void TestMissingV8Snapshot() {
    TempDir td;
    WriteFile(td.Join("libcef.so"), "fake");
    WriteFile(td.Join("icudtl.dat"), "fake");
    WriteFile(td.Join("Resources/chrome_100_percent.pak"), "fake");
    WriteFile(td.Join("Resources/chrome_200_percent.pak"), "fake");
    WriteFile(td.Join("Resources/resources.pak"), "fake");
    auto r = wails_cef::ValidateCefDistribution(td.path);
    EXPECT(!r.ok);
    EXPECT(r.error_code == "missing_file");
    EXPECT(r.missing_path.find("v8_context_snapshot.bin") != std::string::npos);
}

void TestMissingPak() {
    TempDir td;
    WriteFile(td.Join("libcef.so"), "fake");
    WriteFile(td.Join("icudtl.dat"), "fake");
    WriteFile(td.Join("Resources/v8_context_snapshot.bin"), "fake");
    WriteFile(td.Join("Resources/chrome_200_percent.pak"), "fake");
    WriteFile(td.Join("Resources/resources.pak"), "fake");
    auto r = wails_cef::ValidateCefDistribution(td.path);
    EXPECT(!r.ok);
    EXPECT(r.error_code == "missing_file");
    EXPECT(r.missing_path.find("chrome_100_percent.pak") != std::string::npos);
}

void TestMissingIcu() {
    TempDir td;
    WriteFile(td.Join("libcef.so"), "fake");
    WriteFile(td.Join("Resources/v8_context_snapshot.bin"), "fake");
    WriteFile(td.Join("Resources/chrome_100_percent.pak"), "fake");
    WriteFile(td.Join("Resources/chrome_200_percent.pak"), "fake");
    WriteFile(td.Join("Resources/resources.pak"), "fake");
    auto r = wails_cef::ValidateCefDistribution(td.path);
    EXPECT(!r.ok);
    EXPECT(r.error_code == "missing_file");
    EXPECT(r.missing_path.find("icudtl.dat") != std::string::npos);
}

void TestValidDistribution() {
    TempDir td;
    td.TouchValidDistribution();
    auto r = wails_cef::ValidateCefDistribution(td.path);
    EXPECT(r.ok);
    EXPECT(r.error_code.empty());
    EXPECT(r.missing_path.empty());
    EXPECT(r.cef_dir == td.path);
    EXPECT(r.version_major == 147);
}

void TestTrailingSlash() {
    TempDir td;
    td.TouchValidDistribution();
    auto r = wails_cef::ValidateCefDistribution(td.path + "/");
    EXPECT(r.ok);
    EXPECT(r.cef_dir == td.path + "/");
}

void TestFormatValidationError() {
    wails_cef::ValidationResult r;
    r.error_code = "missing_libcef";
    r.error_message = "libcef.so not found";
    auto s = wails_cef::FormatValidationError(r);
    EXPECT(s.find("missing_libcef") != std::string::npos);
    EXPECT(s.find("libcef.so not found") != std::string::npos);

    r.ok = true;
    r.cef_dir = "/opt/cef";
    auto s2 = wails_cef::FormatValidationError(r);
    EXPECT(s2.find("/opt/cef") != std::string::npos);
}

}  // namespace

int main() {
    TestEmptyPath();
    TestNonexistentPath();
    TestMissingLibcef();
    TestMissingV8Snapshot();
    TestMissingPak();
    TestMissingIcu();
    TestValidDistribution();
    TestTrailingSlash();
    TestFormatValidationError();

    std::cout << "validate_cef_test: " << (g_total - g_failures) << "/"
              << g_total << " checks passed" << std::endl;
    return g_failures == 0 ? 0 : 1;
}