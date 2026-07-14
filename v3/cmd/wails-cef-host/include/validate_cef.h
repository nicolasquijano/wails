#pragma once

#include <string>

namespace wails_cef {

struct ValidationResult {
    bool ok = false;
    std::string cef_dir;
    int version_major = 0;
    std::string error_code;
    std::string error_message;
    std::string missing_path;
};

// Validate that the directory at `cef_dir` looks like a CEF 147 runtime
// installation. Returns a structured result so callers can decide how to
// surface the failure. The check is the same one used by download-cef.sh
// and Decision C16: libcef.so, icudtl.dat, Resources/v8_context_snapshot.bin,
// Resources/chrome_100_percent.pak, Resources/chrome_200_percent.pak,
// Resources/resources.pak. The set of locales is not enumerated here.
ValidationResult ValidateCefDistribution(const std::string& cef_dir);

// Human-readable single-line summary suitable for stderr.
std::string FormatValidationError(const ValidationResult& result);

}  // namespace wails_cef