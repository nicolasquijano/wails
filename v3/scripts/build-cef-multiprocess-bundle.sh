#!/usr/bin/env bash
# build-cef-multiprocess-bundle.sh — Build the CEF multi-process bundle
# (Decision C18 M7).
#
# Produces a self-contained directory at $OUT_DIR containing:
#   run.sh                          # wrapper that exports LD_LIBRARY_PATH
#   bin/<app>                       # Go example app (-tags cef)
#   bin/wails-cef-host              # C++ browser process
#   bin/wails-go-runtime            # Go sidecar
#   lib/libcef.so, libEGL.so, libGLESv2.so
#   icudtl.dat
#   Resources/v8_context_snapshot.bin, *.pak, locales/
#
# Usage:
#   CEF_DIR=/path/to/cef ./scripts/build-cef-multiprocess-bundle.sh
#   CEF_DIR=/path/to/cef EXAMPLE=cef-multiwin \
#     ./scripts/build-cef-multiprocess-bundle.sh --out-dir ./dist/bundle
#
# Requirements: cmake, gcc/g++, go (>= 1.22), patchelf, pkg-config.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
V3_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Defaults
CEF_DIR="${CEF_DIR:-}"
EXAMPLE="${EXAMPLE:-cef-hello}"
OUT_DIR="${OUT_DIR:-${V3_ROOT}/dist/cef-bundle}"
BUILD_TYPE="${BUILD_TYPE:-Release}"
JOBS="$(nproc 2>/dev/null || echo 4)"
SKIP_HOST_BUILD="${SKIP_HOST_BUILD:-0}"
SKIP_SIDECAR_BUILD="${SKIP_SIDECAR_BUILD:-0}"
SKIP_APP_BUILD="${SKIP_APP_BUILD:-0}"
CLEAN="${CLEAN:-0}"

# CLI args
while [[ $# -gt 0 ]]; do
    case "$1" in
        --cef-dir)        CEF_DIR="$2"; shift 2;;
        --example)        EXAMPLE="$2"; shift 2;;
        --out-dir)        OUT_DIR="$2"; shift 2;;
        --build-type)     BUILD_TYPE="$2"; shift 2;;
        --jobs)           JOBS="$2"; shift 2;;
        --skip-host)      SKIP_HOST_BUILD=1; shift;;
        --skip-sidecar)   SKIP_SIDECAR_BUILD=1; shift;;
        --skip-app)       SKIP_APP_BUILD=1; shift;;
        --clean)          CLEAN=1; shift;;
        -h|--help)
            sed -n '2,19p' "$0" | sed 's/^# \?//'
            exit 0;;
        *)
            echo "Unknown argument: $1" >&2
            exit 64;;
    esac
done

log() {
    printf '==> %s\n' "$*"
}

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1 (install with your package manager)"
}

# ----------------------------------------------------------------------------
# Pre-flight: tools
# ----------------------------------------------------------------------------
require_cmd cmake
require_cmd g++
require_cmd go
require_cmd patchelf
require_cmd pkg-config

# ----------------------------------------------------------------------------
# Pre-flight: CEF distribution
# ----------------------------------------------------------------------------
if [[ -z "${CEF_DIR}" ]]; then
    fail "CEF_DIR is not set; export CEF_DIR=/path/to/cef (see v3/docs/guides/cef.md)"
fi
if [[ ! -d "${CEF_DIR}" ]]; then
    fail "CEF_DIR does not exist or is not a directory: ${CEF_DIR}"
fi

REQUIRED_CEF_FILES=(
    "libcef.so"
    "icudtl.dat"
    "Resources/v8_context_snapshot.bin"
    "Resources/chrome_100_percent.pak"
    "Resources/chrome_200_percent.pak"
    "Resources/resources.pak"
)
for rel in "${REQUIRED_CEF_FILES[@]}"; do
    if [[ ! -f "${CEF_DIR}/${rel}" ]]; then
        fail "CEF distribution missing required file: ${CEF_DIR}/${rel} (see Decision C16)"
    fi
done

# SDK headers/libcef_dll_wrapper for host build
if [[ "${SKIP_HOST_BUILD}" != "1" ]]; then
    if [[ ! -d "${CEF_DIR}/include" ]]; then
        fail "CEF SDK headers missing: ${CEF_DIR}/include (download-cef.sh preserves SDK under CEF_DIR/sdk and copies headers into the runtime tree, or run download-cef.sh again)"
    fi
    if [[ ! -f "${CEF_DIR}/build/libcef_dll_wrapper/libcef_dll_wrapper.a" ]]; then
        fail "CEF libcef_dll_wrapper.a missing: ${CEF_DIR}/build/libcef_dll_wrapper/libcef_dll_wrapper.a (re-download CEF binary distribution or build libcef_dll_wrapper)"
    fi
fi

log "CEF distribution: ${CEF_DIR}"
log "Example:          ${EXAMPLE}"
log "Output:           ${OUT_DIR}"
log "Build type:       ${BUILD_TYPE}"
log "Jobs:             ${JOBS}"

# ----------------------------------------------------------------------------
# Layout
# ----------------------------------------------------------------------------
if [[ "${CLEAN}" == "1" && -d "${OUT_DIR}" ]]; then
    log "Cleaning ${OUT_DIR}"
    rm -rf "${OUT_DIR}"
fi

BIN_DIR="${OUT_DIR}/bin"
LIB_DIR="${OUT_DIR}/lib"
RES_DIR="${OUT_DIR}/Resources"
mkdir -p "${BIN_DIR}" "${LIB_DIR}" "${RES_DIR}"

# ----------------------------------------------------------------------------
# Step 1: build wails-cef-host (CMake)
# ----------------------------------------------------------------------------
HOST_BIN="${BIN_DIR}/wails-cef-host"
if [[ "${SKIP_HOST_BUILD}" == "1" ]]; then
    log "Skipping host build (SKIP_HOST_BUILD=1)"
elif [[ -x "${HOST_BIN}" && "${CLEAN}" != "1" ]]; then
    log "Host binary already present, skipping (use --clean to force rebuild)"
else
    log "Building wails-cef-host..."
    HOST_BUILD_DIR="${V3_ROOT}/cmd/wails-cef-host/build"
    mkdir -p "${HOST_BUILD_DIR}"
    cmake -S "${V3_ROOT}/cmd/wails-cef-host" \
          -B "${HOST_BUILD_DIR}" \
          -DCEF_ROOT="${CEF_DIR}" \
          -DCMAKE_BUILD_TYPE="${BUILD_TYPE}" \
          > "${HOST_BUILD_DIR}/configure.log" 2>&1 \
        || { tail -50 "${HOST_BUILD_DIR}/configure.log" >&2; fail "cmake configure failed"; }
    cmake --build "${HOST_BUILD_DIR}" --parallel "${JOBS}" \
          > "${HOST_BUILD_DIR}/build.log" 2>&1 \
        || { tail -50 "${HOST_BUILD_DIR}/build.log" >&2; fail "host build failed"; }
    cp "${HOST_BUILD_DIR}/wails-cef-host" "${HOST_BIN}"
    chmod 0755 "${HOST_BIN}"
fi

# ----------------------------------------------------------------------------
# Step 2: build wails-go-runtime
# ----------------------------------------------------------------------------
SIDECAR_BIN="${BIN_DIR}/wails-go-runtime"
if [[ "${SKIP_SIDECAR_BUILD}" == "1" ]]; then
    log "Skipping sidecar build (SKIP_SIDECAR_BUILD=1)"
elif [[ -x "${SIDECAR_BIN}" && "${CLEAN}" != "1" ]]; then
    log "Sidecar binary already present, skipping (use --clean to force rebuild)"
else
    log "Building wails-go-runtime..."
    (
        cd "${V3_ROOT}/cmd/wails-go-runtime"
        # The sidecar's go.mod has `replace github.com/wailsapp/wails/v3 => ../../`
        # which only resolves when Go workspace mode is disabled. The root
        # go.work uses `./v3`, so the relative replace from inside
        # cmd/wails-go-runtime points outside the workspace; GOWORK=off makes
        # go fall back to the per-module replace directive.
        GOWORK=off go build -trimpath -ldflags="-s -w" -o "${SIDECAR_BIN}" .
    )
    chmod 0755 "${SIDECAR_BIN}"
fi

# ----------------------------------------------------------------------------
# Step 3: build example app (-tags cef)
# ----------------------------------------------------------------------------
APP_BIN="${BIN_DIR}/${EXAMPLE}"
if [[ "${SKIP_APP_BUILD}" == "1" ]]; then
    log "Skipping app build (SKIP_APP_BUILD=1)"
elif [[ -x "${APP_BIN}" && "${CLEAN}" != "1" ]]; then
    log "App binary already present, skipping (use --clean to force rebuild)"
else
    EXAMPLE_DIR="${V3_ROOT}/examples/${EXAMPLE}"
    if [[ ! -d "${EXAMPLE_DIR}" ]]; then
        fail "example not found: ${EXAMPLE_DIR}"
    fi
    log "Building ${EXAMPLE} (-tags cef)..."
    (
        cd "${EXAMPLE_DIR}"
        go build -trimpath -ldflags="-s -w" -tags cef -o "${APP_BIN}" .
    )
    chmod 0755 "${APP_BIN}"
fi

# ----------------------------------------------------------------------------
# Step 4: stage CEF runtime
# ----------------------------------------------------------------------------
log "Staging CEF runtime files..."
cp -v "${CEF_DIR}/libcef.so"        "${LIB_DIR}/libcef.so"
# icudtl.dat must sit next to libcef.so (CEF issue #3778). Copy it both
# to lib/ (so libcef.so finds it via dlopen's $ORIGIN) and to the bundle
# root (so the run.sh wrapper can export ICUDTL_PATH for tooling).
cp -v "${CEF_DIR}/icudtl.dat"       "${LIB_DIR}/icudtl.dat"
cp -v "${CEF_DIR}/icudtl.dat"       "${OUT_DIR}/icudtl.dat"
[[ -f "${CEF_DIR}/libEGL.so" ]]      && cp -v "${CEF_DIR}/libEGL.so"      "${LIB_DIR}/"
[[ -f "${CEF_DIR}/libGLESv2.so" ]]   && cp -v "${CEF_DIR}/libGLESv2.so"   "${LIB_DIR}/"
[[ -f "${CEF_DIR}/libvk_swiftshader.so" ]] && cp -v "${CEF_DIR}/libvk_swiftshader.so" "${LIB_DIR}/" || true

for pak in v8_context_snapshot.bin chrome_100_percent.pak chrome_200_percent.pak resources.pak; do
    if [[ -f "${CEF_DIR}/Resources/${pak}" ]]; then
        cp -v "${CEF_DIR}/Resources/${pak}" "${RES_DIR}/"
    fi
done
if [[ -d "${CEF_DIR}/Resources/locales" ]]; then
    cp -r "${CEF_DIR}/Resources/locales" "${RES_DIR}/"
fi

# ----------------------------------------------------------------------------
# Step 5: $ORIGIN rpath for the host binary
# ----------------------------------------------------------------------------
if [[ -x "${HOST_BIN}" ]]; then
    log "Setting RPATH on wails-cef-host..."
    patchelf --set-rpath '$ORIGIN/../lib' "${HOST_BIN}"
fi

# ----------------------------------------------------------------------------
# Step 6: write run.sh wrapper
# ----------------------------------------------------------------------------
cat > "${OUT_DIR}/run.sh" <<'WRAPPER'
#!/usr/bin/env bash
# Run the multi-process CEF bundle. Resolves LD_LIBRARY_PATH relative to
# the script location so the bundle is portable.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export LD_LIBRARY_PATH="${HERE}/lib:${LD_LIBRARY_PATH:-}"
export RESOURCES_DIR="${HERE}/Resources"
export ICUDTL_PATH="${HERE}/icudtl.dat"
exec "${HERE}/bin/${EXAMPLE:-cef-hello}" "$@"
WRAPPER
chmod 0755 "${OUT_DIR}/run.sh"

# ----------------------------------------------------------------------------
# Step 7: write README.txt for the bundle
# ----------------------------------------------------------------------------
cat > "${OUT_DIR}/README.txt" <<README
Wails CEF multi-process bundle
==============================

Layout:
  run.sh                Start the application.
  bin/<app>             Go application binary (built with -tags cef).
  bin/wails-cef-host    C++ browser process.
  bin/wails-go-runtime  Go sidecar (MessageProcessor, assets, services).
  lib/libcef.so         Chromium Embedded Framework runtime.
  lib/libEGL.so,
  lib/libGLESv2.so      GPU libraries loaded by libcef.so.
  icudtl.dat            ICU data (must sit beside libcef.so — CEF issue #3778).
  Resources/            V8 snapshot, *.pak, locales/.

Usage:
  ./run.sh              # launches bin/${EXAMPLE}

Environment overrides (set BEFORE running):
  CEF_DIR               If unset, the bundle uses its staged libcef.so.
  WAILS_CEF_MULTIPROCESS=1   Required to enable the new multi-process backend.
                              Without it the legacy single-process path is used.

Troubleshooting:
  "libcef.so: cannot open shared object" — the wrapper already sets
    LD_LIBRARY_PATH; check that lib/libcef.so is present and readable.
  "Cannot use V8 Proxy resolver in single process mode" — harmless.
  "nVidia device named: nvidia-drm" — harmless.
  For verbose CEF logs: ./run.sh --enable-logging=stderr --v=1

Built on: $(date -u +%Y-%m-%dT%H:%M:%SZ)
Example:  ${EXAMPLE}
README

log "Bundle ready at: ${OUT_DIR}"
log "Run with: ${OUT_DIR}/run.sh"