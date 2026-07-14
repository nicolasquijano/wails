// validate_cef.cc — runtime validation of a CEF 147 distribution.
//
// Called by wails-cef-host before CefInitialize. Returns a structured
// result so the host can fail fast with an actionable error instead of
// crashing deep inside libcef.so. The hard requirements are documented
// in Decision C16 (file layout) and Decision C18 (M1 acceptance).

#include "validate_cef.h"

#include <cstdio>
#include <cstring>
#include <string>
#include <sys/stat.h>
#include <vector>

namespace wails_cef {

namespace {

constexpr const char* kRequiredFiles[] = {
    "libcef.so",
    "icudtl.dat",
    "Resources/v8_context_snapshot.bin",
    "Resources/chrome_100_percent.pak",
    "Resources/chrome_200_percent.pak",
    "Resources/resources.pak",
};

bool FileExists(const std::string& path) {
    struct stat st;
    if (stat(path.c_str(), &st) != 0) {
        return false;
    }
    return S_ISREG(st.st_mode);
}

bool IsExecutable(const std::string& path) {
    struct stat st;
    if (stat(path.c_str(), &st) != 0) {
        return false;
    }
    return S_ISREG(st.st_mode) && (st.st_mode & S_IXUSR);
}

std::string JoinPath(const std::string& a, const std::string& b) {
    if (a.empty()) {
        return b;
    }
    if (a.back() == '/') {
        return a + b;
    }
    return a + "/" + b;
}

bool IsCef147Layout(const std::string& cef_dir, ValidationResult* result) {
    for (const char* rel : kRequiredFiles) {
        std::string full = JoinPath(cef_dir, rel);
        if (!FileExists(full)) {
            result->ok = false;
            result->error_code = "missing_file";
            result->error_message = std::string("required CEF file not found: ") + full;
            result->missing_path = full;
            return false;
        }
    }
    result->cef_dir = cef_dir;
    result->version_major = 147;
    return true;
}

}  // namespace

ValidationResult ValidateCefDistribution(const std::string& cef_dir) {
    ValidationResult result;
    result.ok = false;
    result.version_major = 0;

    if (cef_dir.empty()) {
        result.error_code = "empty_path";
        result.error_message = "CEF_DIR is empty; set CEF_DIR=/path/to/cef";
        return result;
    }

    struct stat st;
    if (stat(cef_dir.c_str(), &st) != 0 || !S_ISDIR(st.st_mode)) {
        result.error_code = "not_a_directory";
        result.error_message = "CEF_DIR does not exist or is not a directory: " + cef_dir;
        return result;
    }

    if (!FileExists(JoinPath(cef_dir, "libcef.so"))) {
        result.error_code = "missing_libcef";
        result.error_message = "libcef.so not found in " + cef_dir +
                               "; install CEF 147 or check CEF_DIR";
        return result;
    }

    if (!IsCef147Layout(cef_dir, &result)) {
        return result;
    }

    result.ok = true;
    return result;
}

std::string FormatValidationError(const ValidationResult& result) {
    if (result.ok) {
        return "CEF distribution OK at " + result.cef_dir;
    }
    std::string out = "wails-cef-host: CEF validation failed (";
    out += result.error_code;
    out += "): ";
    out += result.error_message;
    if (!result.missing_path.empty()) {
        out += " [path=";
        out += result.missing_path;
        out += "]";
    }
    return out;
}

}  // namespace wails_cef