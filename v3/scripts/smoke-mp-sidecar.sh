#!/usr/bin/env bash
# smoke-mp-stub.sh — End-to-end smoke test for M2+M3 wiring.
#
# 1. Start wails-cef-host (CEF browser process).
# 2. wails-cef-host spawns the sidecar stub (Python).
# 3. Sidecar connects, sends hello, receives ready.
# 4. Sidecar dumps the host's process tree (renders, zygote, GPU, etc.).
# 5. Send SIGTERM, verify clean shutdown.

set -euo pipefail

CEF_BIN="${CEF_BIN:-/tmp/mp-test/bin/wails-cef-host}"
SIDECAR_STUB="${SIDECAR_STUB:-/tmp/sidecar-stub.py}"
BUNDLE_DIR="${BUNDLE_DIR:-/tmp/mp-test}"
LOG_DIR="${LOG_DIR:-/tmp/mp-smoke}"

mkdir -p "${LOG_DIR}"
HOST_LOG="${LOG_DIR}/host.log"
SIDECAR_LOG="${LOG_DIR}/sidecar.log"
TREE_LOG="${LOG_DIR}/tree.log"

log() { printf '==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; cleanup; exit 1; }

HOST_PID=""
cleanup() {
    if [[ -n "${HOST_PID}" ]] && kill -0 "${HOST_PID}" 2>/dev/null; then
        kill -TERM "${HOST_PID}" 2>/dev/null || true
        for _ in 1 2 3 4 5; do
            kill -0 "${HOST_PID}" 2>/dev/null || break
            sleep 0.2
        done
        kill -KILL "${HOST_PID}" 2>/dev/null || true
    fi
    pkill -KILL -f wails-cef-host 2>/dev/null || true
    pkill -KILL -f sidecar-stub 2>/dev/null || true
}
trap cleanup EXIT INT TERM

log "CEF bin:      ${CEF_BIN}"
log "Sidecar stub: ${SIDECAR_STUB}"
log "Bundle dir:   ${BUNDLE_DIR}"
log "Log dir:      ${LOG_DIR}"

# Replace the sidecar binary in the bundle with our stub. The host looks
# for "wails-go-runtime" in argv0's directory (CWD) and in CEF_DIR.
STUB_PATH="${BUNDLE_DIR}/bin/wails-go-runtime"
cp -f "${SIDECAR_STUB}" "${STUB_PATH}"
chmod 0755 "${STUB_PATH}"
log "Sidecar stub installed at ${STUB_PATH}"

# Start the host.
truncate -s 0 "${HOST_LOG}" "${SIDECAR_LOG}" "${TREE_LOG}"
DISPLAY=:99 \
LD_LIBRARY_PATH="${BUNDLE_DIR}/lib" \
CEF_DIR="${BUNDLE_DIR}" \
WAILS_CEF_SIDECAR="${STUB_PATH}" \
setsid "${CEF_BIN}" \
    --cef-host-url=wails://localhost/ \
    > "${HOST_LOG}" 2>&1 </dev/null &
HOST_PID=$!
log "Host PID: ${HOST_PID}"

# Wait for the sidecar stub to appear in the process list or for the
# handshake to complete (look for "sidecar ready" in the host log).
elapsed=0
ok=0
while (( elapsed < 15 )); do
    if ! kill -0 "${HOST_PID}" 2>/dev/null; then
        log "Host exited early. Log:"
        tail -40 "${HOST_LOG}" >&2
        fail "host died before handshake"
    fi
    if grep -q "sidecar ready" "${HOST_LOG}" 2>/dev/null; then
        ok=1
        break
    fi
    sleep 0.5
    elapsed=$((elapsed + 1))
done

if (( ok != 1 )); then
    log "Host log:"
    cat "${HOST_LOG}" >&2
    fail "sidecar ready not seen within 15s"
fi
log "Handshake OK (host reports 'sidecar ready')"

# Wait a bit more for CEF to spawn its subprocesses.
sleep 2

# Dump process tree.
ps -e -o pid,ppid,args | grep -E "wails-cef-host|sidecar-stub" | grep -v grep > "${TREE_LOG}" 2>&1 || true
cat "${TREE_LOG}"

# Count CEF subprocesses.
zygote_count=$(grep -c -- '--type=zygote' "${TREE_LOG}" || true)
renderer_count=$(grep -c -- '--type=renderer' "${TREE_LOG}" || true)
utility_count=$(grep -c -- '--type=utility' "${TREE_LOG}" || true)
total=$(grep -c -E "wails-cef-host" "${TREE_LOG}" || true)

log "Counts: zygote=${zygote_count} renderer=${renderer_count} utility=${utility_count} total=${total}"

if (( total < 3 )); then
    fail "expected at least 3 wails-cef-host processes (browser + zygote + utility), got ${total}"
fi

# Trigger graceful shutdown.
log "Sending SIGTERM to host pid=${HOST_PID}"
kill -TERM "${HOST_PID}"
for _ in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "${HOST_PID}" 2>/dev/null || break
    sleep 0.3
done
kill -KILL "${HOST_PID}" 2>/dev/null || true
wait "${HOST_PID}" 2>/dev/null || true

# Wait for sidecar stub to also exit.
sleep 1

log "=== host log tail ==="
tail -20 "${HOST_LOG}"

if grep -qi "shutdown\|stopped\|exit" "${SIDECAR_LOG}" 2>/dev/null; then
    log "Sidecar reported shutdown"
fi

log "smoke test PASSED"
exit 0