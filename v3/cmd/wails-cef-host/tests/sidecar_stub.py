#!/usr/bin/env python3
"""
Test stub for wails-go-runtime. Verifies the M2+M3 handshake.

Used by the multi-process CEF smoke test. Mirrors the real sidecar's
handshake (connect, send hello, read ready, idle, send shutdown on signal)
and additionally prints the descendant tree of the host process so the
test can assert that multi-process CEF actually spawned subprocesses.
"""
import argparse
import json
import os
import signal
import socket
import struct
import subprocess
import sys
import time


def send_frame(sock, payload):
    header = struct.pack(">I", len(payload))
    sock.sendall(header + payload.encode("utf-8"))


def recv_frame(sock):
    hdr = b""
    while len(hdr) < 4:
        chunk = sock.recv(4 - len(hdr))
        if not chunk:
            return None
        hdr += chunk
    length = struct.unpack(">I", hdr)[0]
    body = b""
    while len(body) < length:
        chunk = sock.recv(length - len(body))
        if not chunk:
            return None
        body += chunk
    return body.decode("utf-8")


def list_cef_descendants(host_pid):
    descendants = []
    try:
        out = subprocess.check_output(
            ["ps", "-e", "-o", "pid=,ppid=,args="],
            text=True, timeout=2,
        )
    except Exception:
        return descendants
    children_of = {}
    for line in out.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) < 3:
            continue
        pid, ppid, cmd = parts
        try:
            pid_i, ppid_i = int(pid), int(ppid)
        except ValueError:
            continue
        children_of.setdefault(ppid_i, []).append((pid_i, cmd))

    def walk(pid, depth):
        for child_pid, child_cmd in children_of.get(pid, []):
            ctype = ""
            for tok in child_cmd.split():
                if tok.startswith("--type="):
                    ctype = tok[len("--type="):]
                    break
            descendants.append((child_pid, pid, ctype, depth))
            walk(child_pid, depth + 1)

    walk(host_pid, 1)
    return descendants


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--cef-host-socket", required=True)
    p.add_argument("--cef-host-capability", required=True)
    p.add_argument("--cef-host-protocol", default="1")
    p.add_argument("--assets-dir", default="")
    p.add_argument("--frontend-url", default="")
    p.add_argument("--print-tree", action="store_true")
    p.add_argument("--host-pid", type=int, default=0,
                   help="Host PID for tree dump")
    args = p.parse_args()

    if args.cef_host_protocol != "1":
        print(f"protocol mismatch: got {args.cef_host_protocol}, want 1",
              file=sys.stderr)
        sys.exit(2)

    print(f"sidecar-stub: connecting to {args.cef_host_socket}",
          file=sys.stderr)
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.settimeout(5.0)
    sock.connect(args.cef_host_socket)

    hello = json.dumps({
        "v": 1,
        "kind": 0,
        "capability": args.cef_host_capability,
        "pid": os.getpid(),
    })
    send_frame(sock, hello)
    print("sidecar-stub: hello sent", file=sys.stderr)

    ready = recv_frame(sock)
    if ready is None:
        print("sidecar-stub: peer closed", file=sys.stderr)
        sys.exit(3)
    ready_env = json.loads(ready)
    if ready_env.get("kind") != 1:
        print(f"sidecar-stub: expected ready, got {ready_env}",
              file=sys.stderr)
        sys.exit(4)
    host_pid = ready_env.get("hostPid")
    print(f"sidecar-stub: ready received, host pid={host_pid}",
          file=sys.stderr)

    if args.print_tree:
        time.sleep(0.5)
        descendants = list_cef_descendants(host_pid)
        print(f"sidecar-stub: descendants of pid={host_pid}:",
              file=sys.stderr)
        for pid, ppid, ctype, depth in descendants:
            indent = "  " * depth
            print(f"sidecar-stub: {indent}pid={pid} ppid={ppid} type={ctype}",
                  file=sys.stderr)
        sys.stderr.flush()
        # Emit a single line on stdout that the test can grep for.
        print(f"SPAWNED_CHILDREN={len(descendants)}")

    stop = {"requested": False}

    def handle(signum, frame):
        stop["requested"] = True

    signal.signal(signal.SIGTERM, handle)
    signal.signal(signal.SIGINT, handle)

    while not stop["requested"]:
        signal.pause()

    print("sidecar-stub: shutdown", file=sys.stderr)
    shutdown = json.dumps({
        "v": 1,
        "kind": 6,
        "capability": args.cef_host_capability,
        "reason": "stub_shutdown",
    })
    try:
        send_frame(sock, shutdown)
    except OSError:
        pass
    sock.close()
    sys.exit(0)


if __name__ == "__main__":
    main()