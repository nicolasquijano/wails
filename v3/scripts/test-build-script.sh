#!/usr/bin/env bash
# test-build-script.sh — regression tests for build-cef-multiprocess-bundle.sh.
#
# Builds a fake CEF distribution under a temp directory and verifies that
# the bundle script:
#   - aborts with the right error code when required files are missing
#   - accepts a stub binary in place of wails-cef-host when --skip-host is set
#   - produces the expected layout
#
# Does NOT require CEF/GTK/X11. Does NOT actually compile anything C++.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_SCRIPT="${SCRIPT_DIR}/build-cef-multiprocess-bundle.sh"
V3_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

EXAMPLE="${EXAMPLE:-cef-hello}"
G_FAILURES=0
G_TOTAL=0

expect() {
    local label="$1"
    local actual="$2"
    local want="$3"
    G_TOTAL=$((G_TOTAL + 1))
    if [[ "${actual}" == "${want}" ]]; then
        printf '  ok    %-40s == %s\n' "${label}" "${want}"
    else
        printf '  FAIL  %-40s got=%s want=%s\n' "${label}" "${actual}" "${want}" >&2
        G_FAILURES=$((G_FAILURES + 1))
    fi
}

assert_file() {
    local label="$1"
    local path="$2"
    G_TOTAL=$((G_TOTAL + 1))
    if [[ -e "${path}" ]]; then
        printf '  ok    %-40s exists\n' "${label}"
    else
        printf '  FAIL  %-40s missing: %s\n' "${label}" "${path}" >&2
        G_FAILURES=$((G_FAILURES + 1))
    fi
}

assert_exec() {
    local label="$1"
    local path="$2"
    G_TOTAL=$((G_TOTAL + 1))
    if [[ -x "${path}" ]]; then
        printf '  ok    %-40s executable\n' "${label}"
    else
        printf '  FAIL  %-40s not executable: %s\n' "${label}" "${path}" >&2
        G_FAILURES=$((G_FAILURES + 1))
    fi
}

# Build a fake CEF distribution with the layout Decision C16 expects.
make_fake_cef() {
    local dest="$1"
    rm -rf "${dest}"
    mkdir -p "${dest}/Resources"
    touch "${dest}/libcef.so"
    touch "${dest}/icudtl.dat"
    touch "${dest}/Resources/v8_context_snapshot.bin"
    touch "${dest}/Resources/chrome_100_percent.pak"
    touch "${dest}/Resources/chrome_200_percent.pak"
    touch "${dest}/Resources/resources.pak"
}

make_fake_cef_with_sdk() {
    local dest="$1"
    make_fake_cef "${dest}"
    mkdir -p "${dest}/build/libcef_dll_wrapper"
    touch "${dest}/build/libcef_dll_wrapper/libcef_dll_wrapper.a"
    mkdir -p "${dest}/include"
    touch "${dest}/include/.keep"
}

# -----------------------------------------------------------------------------
echo "Test 1: missing CEF_DIR"
set +e
out=$(bash "${BUILD_SCRIPT}" 2>&1)
ec=$?
set -e
# bash `fail` uses `exit 1` for the script
expect "exit code when CEF_DIR unset" "${ec}" "1"
echo "${out}" | grep -q "CEF_DIR is not set" || { G_FAILURES=$((G_FAILURES + 1)); echo "FAIL: expected CEF_DIR error" >&2; }
G_TOTAL=$((G_TOTAL + 1))

# -----------------------------------------------------------------------------
echo "Test 2: CEF_DIR exists but libcef.so is missing"
FAKE=$(mktemp -d)
mkdir -p "${FAKE}/Resources"
set +e
out=$(CEF_DIR="${FAKE}" bash "${BUILD_SCRIPT}" 2>&1)
ec=$?
set -e
expect "exit code when libcef.so missing" "${ec}" "1"
echo "${out}" | grep -q "missing required file" || { G_FAILURES=$((G_FAILURES + 1)); echo "FAIL: expected missing-file error" >&2; }
G_TOTAL=$((G_TOTAL + 1))
rm -rf "${FAKE}"

# -----------------------------------------------------------------------------
echo "Test 3: CEF_DIR has libcef.so but no v8 snapshot (SDK present)"
FAKE=$(mktemp -d)
make_fake_cef_with_sdk "${FAKE}"
rm -f "${FAKE}/Resources/v8_context_snapshot.bin"
# Skip host build so the SDK check is bypassed and we reach the file check.
set +e
out=$(CEF_DIR="${FAKE}" SKIP_HOST_BUILD=1 SKIP_SIDECAR_BUILD=1 SKIP_APP_BUILD=1 \
        bash "${BUILD_SCRIPT}" 2>&1)
ec=$?
set -e
expect "exit code when v8_context_snapshot missing" "${ec}" "1"
echo "${out}" | grep -q "v8_context_snapshot.bin" || { G_FAILURES=$((G_FAILURES + 1)); echo "FAIL: expected v8_context_snapshot error" >&2; }
G_TOTAL=$((G_TOTAL + 1))
rm -rf "${FAKE}"

# -----------------------------------------------------------------------------
echo "Test 4: CEF_DIR valid; full bundle with skip flags"
FAKE=$(mktemp -d)
make_fake_cef_with_sdk "${FAKE}"
OUT=$(mktemp -d)

# We need a valid Go app to "build". Use a tiny throwaway module so the build
# step succeeds. Sidecar is a real Go module; we skip its build to avoid
# needing the workspace replace directive. The app build is replaced by a
# tiny throwaway package so we don't depend on the full example tree.
APP_DIR=$(mktemp -d)
cat > "${APP_DIR}/go.mod" <<EOF
module example.com/throwaway
go 1.22
EOF
cat > "${APP_DIR}/main.go" <<EOF
package main
func main() {}
EOF

# Stage prebuilt stubs in the bundle so the script picks them up.
# The host stub must be a real ELF so patchelf can rewrite its rpath.
mkdir -p "${OUT}/bin"
# Compile a tiny C++ stub ELF. patchelf refuses non-ELF inputs.
echo 'int main(){return 0;}' > /tmp/stub_host.cpp
g++ -O2 -o "${OUT}/bin/wails-cef-host" /tmp/stub_host.cpp
cat > "${OUT}/bin/wails-go-runtime" <<'EOF'
#!/usr/bin/env bash
echo "stub sidecar"
EOF
chmod 0755 "${OUT}/bin/wails-go-runtime"
rm -f /tmp/stub_host.cpp

# Override the example dir by pointing EXAMPLE at a path that doesn't exist
# and using SKIP_APP_BUILD=1 to avoid building it. Same for the host build.
CEF_DIR="${FAKE}" OUT_DIR="${OUT}" EXAMPLE="throwaway" \
    SKIP_HOST_BUILD=1 SKIP_SIDECAR_BUILD=1 SKIP_APP_BUILD=1 \
    bash "${BUILD_SCRIPT}" > /tmp/build-script-test.log 2>&1 || {
    cat /tmp/build-script-test.log >&2
    G_FAILURES=$((G_FAILURES + 1))
    echo "FAIL: bundle script exited non-zero" >&2
}
G_TOTAL=$((G_TOTAL + 1))

assert_file "bundle/run.sh"            "${OUT}/run.sh"
assert_exec "bundle/run.sh executable" "${OUT}/run.sh"
assert_file "bundle/icudtl.dat"        "${OUT}/icudtl.dat"
assert_file "bundle/lib/libcef.so"     "${OUT}/lib/libcef.so"
assert_file "bundle/Resources/v8_context_snapshot.bin" "${OUT}/Resources/v8_context_snapshot.bin"
assert_file "bundle/Resources/chrome_100_percent.pak"  "${OUT}/Resources/chrome_100_percent.pak"
assert_file "bundle/README.txt"        "${OUT}/README.txt"

# Confirm the host binary's RPATH was set.
if command -v patchelf >/dev/null 2>&1 && [[ -x "${OUT}/bin/wails-cef-host" ]]; then
    rpath=$(patchelf --print-rpath "${OUT}/bin/wails-cef-host" 2>/dev/null || true)
    G_TOTAL=$((G_TOTAL + 1))
    if [[ "${rpath}" == *'$ORIGIN/../lib'* || "${rpath}" == *'$ORIGIN/../lib'* ]]; then
        printf '  ok    host rpath includes $ORIGIN/../lib\n'
    else
        printf '  FAIL  host rpath=%q\n' "${rpath}" >&2
        G_FAILURES=$((G_FAILURES + 1))
    fi
fi

rm -rf "${FAKE}" "${OUT}" "${APP_DIR}"

# -----------------------------------------------------------------------------
echo
echo "summary: $((G_TOTAL - G_FAILURES))/${G_TOTAL} passed"
exit "${G_FAILURES}"