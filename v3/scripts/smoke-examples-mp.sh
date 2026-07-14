#!/usr/bin/env bash
# smoke-examples-mp.sh — Demonstrates the multi-process CEF backend by
# running each example with WAILS_CEF_MULTIPROCESS=1 and verifying that
# the resulting process tree contains wails-cef-host descendants
# (zygote, utility, renderer, plus the Go sidecar wails-go-runtime).
#
# Requires:
#   - Xvfb running on $DISPLAY (default :99)
#   - bundle at $BUNDLE_DIR with bin/{cef-hello,wails-cef-host,wails-go-runtime}
#   - libcef.so + icudtl.dat + Resources/ in the bundle

set -euo pipefail

BUNDLE_DIR="${BUNDLE_DIR:-/tmp/mp-test}"
DISPLAY="${DISPLAY:-:99}"
WAIT_SECONDS="${WAIT_SECONDS:-8}"
LOG_DIR="${LOG_DIR:-/tmp/mp-examples}"

mkdir -p "${LOG_DIR}"
TOTAL_OK=0
TOTAL_FAIL=0

cleanup_proc() {
    local label="$1"
    pkill -KILL -f "/${label}" 2>/dev/null || true
    pkill -KILL -f wails-cef-host 2>/dev/null || true
    sleep 1
}

# run_example <bin> [<skip-if-file-missing>]
# If the second arg is a path that does not exist, the example is skipped
# (used for cef-shadcn-admin which requires the frontend dist to be built).
run_example() {
    local bin="$1"
    local skip_if_missing="${2:-}"
    local label="${bin}"
    local log="${LOG_DIR}/${label}.log"
    local procs="${LOG_DIR}/${label}.procs"

    if [[ -n "${skip_if_missing}" && ! -e "${skip_if_missing}" ]]; then
        printf 'skip %s (asset missing: %s)\n' "${label}" "${skip_if_missing}"
        return 0
    fi

    cleanup_proc "${bin}"

    printf '\n=========================================\n'
    printf '== example: %s\n' "${label}"
    printf '=========================================\n'

    DISPLAY="${DISPLAY}" \
    LD_LIBRARY_PATH="${BUNDLE_DIR}/lib" \
    CEF_DIR="${BUNDLE_DIR}" \
    WAILS_CEF_MULTIPROCESS=1 \
    setsid "${BUNDLE_DIR}/bin/${bin}" \
        > "${log}" 2>&1 </dev/null &
    local pid=$!
    printf 'launched PID=%d, waiting %ds for tree...\n' "${pid}" "${WAIT_SECONDS}"
    sleep "${WAIT_SECONDS}"

    if ! kill -0 "${pid}" 2>/dev/null; then
        printf 'FAIL: example exited before tree appeared; tail of log:\n'
        tail -20 "${log}"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
        return 1
    fi

    # Dump the tree. The example's PID is the browser process itself
    # (the execve in the launcher replaces the Go process image with
    # wails-cef-host, so $pid becomes the browser PID). Walk
    # descendants from the browser PID; use Python so we can iterate
    # over child sets without shell quoting pitfalls.
    browser_pid=$(pgrep -f "wails-cef-host --cef-host-url" | head -1 || true)
    if [[ -z "${browser_pid}" ]]; then
        browser_pid="${pid}"
    fi
    python3 - "${browser_pid}" > "${procs}" <<'PY' || true
import os, sys
root = sys.argv[1]
seen = set([root])
queue = [root]
while queue:
    nxt = []
    for pid in queue:
        try:
            with open(f"/proc/{pid}/task/{pid}/children") as f:
                children = f.read().split()
        except OSError:
            children = []
        for c in children:
            if c and c not in seen:
                seen.add(c)
                nxt.append(c)
    queue = nxt
pids = sorted(seen, key=int)
pid_list = ",".join(pids)
out = []
for line in os.popen(f"ps -p {pid_list} -o pid=,ppid=,args= 2>/dev/null"):
    line = line.rstrip("\n")
    if not line.strip():
        continue
    parts = line.split(None, 2)
    if len(parts) < 3:
        continue
    p, pp, cmd = parts
    out.append(f"{p}\t{pp}\t{cmd}")
sys.stdout.write("\n".join(out) + "\n")
PY
    cat "${procs}"

    local browser renderer_count utility_count zygote_count sidecar_count
    browser=$(grep -c -- 'wails-cef-host --cef-host-url' "${procs}" || true)
    zygote_count=$(grep -c -- '--type=zygote' "${procs}" || true)
    renderer_count=$(grep -c -- '--type=renderer' "${procs}" || true)
    utility_count=$(grep -c -- '--type=utility' "${procs}" || true)
    sidecar_count=$(grep -c -- 'wails-go-runtime --cef-host-socket' "${procs}" || true)

    printf '\ncounts: browser=%d zygote=%d renderer=%d utility=%d sidecar=%d\n' \
        "${browser}" "${zygote_count}" "${renderer_count}" "${utility_count}" "${sidecar_count}"

    local fail=0
    if (( browser < 1 )); then
        printf '  FAIL: no browser process (wails-cef-host --cef-host-url)\n'; fail=1; fi
    if (( zygote_count < 1 )); then
        printf '  FAIL: no zygote subprocess\n'; fail=1; fi
    if (( renderer_count < 1 )); then
        printf '  FAIL: no renderer subprocess\n'; fail=1; fi
    if (( utility_count < 1 )); then
        printf '  FAIL: no utility subprocess\n'; fail=1; fi
    if (( sidecar_count < 1 )); then
        printf '  FAIL: no Go sidecar (wails-go-runtime --cef-host-socket)\n'; fail=1; fi

    # Confirm the host log shows the handshake OK.
    if ! grep -q "sidecar ready" "${log}"; then
        printf '  FAIL: host log missing "sidecar ready"\n'; fail=1
    fi

    if (( fail == 0 )); then
        printf 'OK\n'
        TOTAL_OK=$((TOTAL_OK + 1))
    else
        printf 'log tail:\n'
        tail -20 "${log}"
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fi

    cleanup_proc "${bin}"
}

# Order: simple examples first. Each example gets an optional
# "skip-if-missing" path for asset directories that aren't part of this
# repository.
run_example cef-hello-mp
run_example cef-multiwin-mp
run_example cef-shadcn-mp /tmp/shadcn-admin/dist

printf '\n=========================================\n'
printf 'summary: %d ok, %d failed\n' "${TOTAL_OK}" "${TOTAL_FAIL}"
exit "${TOTAL_FAIL}"