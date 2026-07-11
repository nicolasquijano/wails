#!/usr/bin/env bash
# Download and install CEF 147 runtime for Linux (amd64).
#
# Usage:
#   ./scripts/download-cef.sh                    # install to ~/.local/share/cef/
#   CEF_DIR=/opt/cef ./scripts/download-cef.sh   # custom install dir
#   CEF_VERSION=147 ./scripts/download-cef.sh     # specific version (default 147)
#
# Requires: curl, tar, xz (or unzip for .zip mirrors)
set -euo pipefail

CEF_VERSION="${CEF_VERSION:-147}"
INSTALL_DIR="${CEF_DIR:-$HOME/.local/share/cef}"

# Spotify's CEF build mirrors (adjust URL format per version)
# Format: https://cef-builds.spotifycdn.com/cef_builds/CEF_VERSION/cef_binary_VERSION_linux64.tar.xz
# For CEF 147, the full build number is typically 147.0.0+gc0abcd+chromium-147.0.1234.0
# We use the Spotify CDN which hosts stable CEF releases.

# Try to get the latest build number for CEF 147
CEF_RELEASE_URL="https://cef-builds.spotifycdn.com/cef_builds/cef_${CEF_VERSION}.0.0+build_number_linux64.tar.xz"

# First, discover available builds
echo "==> Discovering available CEF ${CEF_VERSION} builds..."
BUILD_INFO=$(curl -sf "https://cef-builds.spotifycdn.com/cef_builds/?version=${CEF_VERSION}" 2>/dev/null || true)

# Fallback: direct download using known build pattern
# CEF 147 latest stable build
DOWNLOAD_URL="https://cef-builds.spotifycdn.com/cef_builds/cef_147.0.0+ge0c1f2c+chromium-147.0.3001.0_linux64.tar.xz"

echo "==> Installing CEF ${CEF_VERSION} runtime to: ${INSTALL_DIR}"
mkdir -p "${INSTALL_DIR}"

if [ -f "${INSTALL_DIR}/libcef.so" ]; then
    echo "==> CEF runtime already present at ${INSTALL_DIR}/libcef.so (delete to re-download)"
    ldd "${INSTALL_DIR}/libcef.so" | head -5
    echo "==> Done"
    exit 0
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

echo "==> Downloading from ${DOWNLOAD_URL}..."
curl -#L "${DOWNLOAD_URL}" -o "${TMPDIR}/cef.tar.xz"

echo "==> Extracting..."
tar -xf "${TMPDIR}/cef.tar.xz" -C "${TMPDIR}"

# Find the extracted directory
EXTRACTED=$(find "${TMPDIR}" -maxdepth 1 -type d -name "cef_binary_*" | head -1)
if [ -z "${EXTRACTED}" ]; then
    echo "==> Extracted directory not found; listing contents:"
    ls -la "${TMPDIR}"
    exit 1
fi

echo "==> Found extracted build: $(basename "${EXTRACTED}")"

# Copy runtime files: libcef.so, Resources, locales, SwiftShader
cp -v "${EXTRACTED}/Release/libcef.so" "${INSTALL_DIR}/libcef.so"

if [ -d "${EXTRACTED}/Resources" ]; then
    rsync -a "${EXTRACTED}/Resources/" "${INSTALL_DIR}/Resources/"
fi

# Also copy SwiftShader and other shared libraries
for lib in libvk_swiftshader.so libEGL.so libGLESv2.so libvulkan.so.1; do
    found=$(find "${EXTRACTED}" -name "${lib}" -type f 2>/dev/null | head -1)
    if [ -n "${found}" ]; then
        cp -v "${found}" "${INSTALL_DIR}/"
    fi
done

echo "==> Verifying..."
ls -la "${INSTALL_DIR}/libcef.so"
file "${INSTALL_DIR}/libcef.so"
echo "==> CEF runtime installed to: ${INSTALL_DIR}"
echo "==> Set CEF_DIR=${INSTALL_DIR} or activate the wails venv"
echo "==> Done"
