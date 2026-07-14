#!/usr/bin/env bash
# smoke-test-bundle.sh — End-to-end smoke test for a built CEF bundle.
#
# Boots the bundle via xvfb, waits for the CEF DevTools port to come up,
# fetches /json/version, verifies the Browser field, then kills the host.
# Intended to be run from CI under `xvfb-run -a` on a machine with a
# working X server (X11 or XWayland).
#
# Usage:
#   xvfb-run -a ./scripts/smoke-test-bundle.sh ./dist/cef-bundle
#   BUNDLE_DIR=./dist/cef-bundle ./scripts/smoke-test-bundle.sh

set -euo pipefail

BUNDLE_DIR="${1:-${BUNDLE_DIR:-}}"
DEVTOOLS_PORT="${DEVTOOLS_PORT:-9999}"
WAIT_SECONDS="${WAIT_SECONDS:-15}"
LOG_FILE="${LOG_FILE:-/tmp/wails-cef-smoke.log}"

if [[ -z "${BUNDLE_DIR}" ]]; then
    echo "usage: $0 <bundle-dir>" >&2
    echo "       BUNDLE_DIR=<bundle-dir> $0" >&2
    exit 64
fi
if [[ ! -d "${BUNDLE_DIR}" ]]; then
    echo "ERROR: bundle directory does not exist: ${BUNDLE_DIR}" >&2
    exit 1
fi
if [[ ! -x "${BUNDLE_DIR}/run.sh" ]]; then
    echo "ERROR: ${BUNDLE_DIR}/run.sh is missing or not executable" >&2
    exit 1
fi

log() { printf '==> %s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; cleanup; exit 1; }

HOST_PID=""
cleanup() {
    if [[ -n "${HOST_PID}" ]] && kill -0 "${HOST_PID}" 2>/dev/null; then
        log "killing host pid=${HOST_PID}"
        kill -TERM "${HOST_PID}" 2>/dev/null || true
        for _ in 1 2 3 4 5; do
            kill -0 "${HOST_PID}" 2>/dev/null || break
            sleep 0.2
        done
        kill -KILL "${HOST_PID}" 2>/dev/null || true
    fi
    # Kill any leftover CEF children still attached to the same pgid.
    if [[ -n "${HOST_PID}" ]]; then
        pkill -KILL -P "${HOST_PID}" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

log "Bundle:   ${BUNDLE_DIR}"
log "Log:      ${LOG_FILE}"
log "DevTools: http://127.0.0.1:${DEVTOOLS_PORT}"

cd "${BUNDLE_DIR}"
: > "${LOG_FILE}"
./run.sh > "${LOG_FILE}" 2>&1 &
HOST_PID=$!
log "host pid: ${HOST_PID}"

# Wait for DevTools port to start responding.
elapsed=0
ok=0
while (( elapsed < WAIT_SECONDS )); do
    if ! kill -0 "${HOST_PID}" 2>/dev/null; then
        log "host exited early; tail of log:"
        tail -50 "${LOG_FILE}" >&2
        fail "host process died before DevTools port came up"
    fi
    if curl --silent --fail --max-time 1 \
            "http://127.0.0.1:${DEVTOOLS_PORT}/json/version" > /tmp/wails-cef-version.json 2>/dev/null; then
        ok=1
        break
    fi
    sleep 0.5
    elapsed=$((elapsed + 1))
done

if (( ok != 1 )); then
    log "DevTools port did not respond within ${WAIT_SECONDS}s; tail of log:"
    tail -80 "${LOG_FILE}" >&2
    fail "DevTools port timeout"
fi

log "DevTools responded"
cat /tmp/wails-cef-version.json

# Validate the response. CEF DevTools reports Browser="Chrome/..." with the
# Chromium version.
if ! grep -q '"Browser"' /tmp/wails-cef-version.json; then
    fail "DevTools response missing Browser field"
fi
if ! grep -q 'Chrome/' /tmp/wails-cef-version.json; then
    fail "DevTools Browser field does not start with 'Chrome/'"
fi

# Fetch /json and ensure at least one target (the main frame).
if ! curl --silent --fail --max-time 2 \
        "http://127.0.0.1:${DEVTOOLS_PORT}/json" > /tmp/wails-cef-targets.json; then
    fail "DevTools /json did not respond"
fi
target_count=$(python3 -c "import json,sys; d=json.load(open('/tmp/wails-cef-targets.json')); print(len(d))" 2>/dev/null \
    || grep -o '"webSocketDebuggerUrl"' /tmp/wails-cef-targets.json | wc -l)
log "Targets: ${target_count}"
if (( target_count < 1 )); then
    fail "DevTools reported no targets"
fi

log "smoke test PASSED"
exit 0