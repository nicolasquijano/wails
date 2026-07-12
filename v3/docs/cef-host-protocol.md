# CEF Native Host Protocol

Status: draft for Decision C18. This document defines the private IPC contract
between `wails-cef-host` (C++ browser process) and `wails-go-runtime` (Go
sidecar). It is Linux-first and must be versioned before either process is
implemented.

## Scope and trust boundary

The C++ host owns CEF, GTK, windows, renderer process messages and every
`CefBrowser`/`CefFrame` pointer. The Go sidecar owns application services,
`MessageProcessor`, assets and application data. Neither side exposes this
socket to web content or the network.

The protocol carries opaque application payloads. C++ validates envelope,
identity and size but does not deserialize Wails business data. Go must never
receive or retain a native CEF/GTK pointer.

## Transport setup

1. The C++ host creates a unique directory below `$XDG_RUNTIME_DIR/wails/`
   with mode `0700`, then listens on `host.sock` with mode `0600`.
2. The host generates a 32-byte random capability, base64url-encodes it, and
   starts the Go sidecar via `posix_spawn` with only these private arguments:
   `--cef-host-socket`, `--cef-host-capability`, `--cef-host-protocol=1`.
3. The host obtains `SO_PEERCRED` for the Unix peer and requires the same UID
   as the host before parsing any frame.
4. Go connects, sends `hello`, waits for `ready`, then accepts requests. A
   failed handshake, wrong UID, wrong capability or protocol mismatch closes
   the connection without retrying.
5. Host shutdown sends `shutdown`, waits up to two seconds, then terminates
   and reaps the sidecar. Sidecar disconnect cancels every in-flight request
   and becomes a renderer-visible backend-unavailable error.

The capability is carried on every frame, not only the handshake. This keeps
the contract safe if a socket file is accidentally reused or a future transport
is multiplexed.

## Framing

Each record is a four-byte unsigned big-endian payload length followed by one
UTF-8 JSON object. `length` excludes the prefix.

- Maximum payload: 16 MiB. Reject a larger prefix before allocating memory.
- Maximum concurrently pending requests per connection: 256.
- Maximum event queue per window: 128; overflow is reported as a dropped-event
  diagnostic and does not grow unbounded.
- A peer must finish one framed record within 10 seconds after its prefix is
  received. This limits slow-peer resource retention.

Binary image/RAW data is never carried in this protocol. Preview and asset data
use an existing file/cache path, a compact encoded payload negotiated by a
separate protocol revision, or a CEF resource stream.

## Envelope schema (version 1)

```json
{
  "v": 1,
  "kind": "request",
  "capability": "base64url-32-byte-token",
  "id": "host-generated-request-id",
  "browserId": 12,
  "frameId": "42",
  "windowId": 7,
  "operation": "runtime.call",
  "deadlineUnixMs": 1773330000000,
  "payload": {}
}
```

Required fields by `kind`:

| Kind | Direction | Required fields | Meaning |
|---|---|---|---|
| `hello` | Go → host | `v`, `capability`, `pid` | Authenticated initial connection. No request ID. |
| `ready` | host → Go | `v`, `capability`, `hostPid` | Go may begin processing after this acknowledgement. |
| `request` | either direction | `id`, `operation`, `payload`, `deadlineUnixMs` | One asynchronous operation. Browser/frame/window identity is mandatory for renderer-originated calls. |
| `response` | reply direction | `id`, `ok`, `payload` or `error` | Exactly one terminal reply for a request. |
| `event` | either direction | `operation`, `payload`, optional target IDs | Unsolicited notification; never reused as a response. |
| `cancel` | either direction | `id`, `reason` | Best-effort cancellation. A terminal response may already be in flight. |
| `shutdown` | host → Go | `reason`, `deadlineUnixMs` | Graceful sidecar termination. |

`id` is an ASCII, host-generated, 128-bit random base64url value. Go-generated
requests use the same format. IDs must not be reused during one connection.
`frameId` is encoded as a string because CEF frame identifiers are 64-bit and
JSON number precision is insufficient in JavaScript-adjacent tooling.

## Operations

Version 1 reserves these namespaces:

| Namespace | Initiator | Owner |
|---|---|---|
| `runtime.call`, `runtime.cancel` | renderer through host | Go `MessageProcessor` |
| `asset.request` | C++ resource handler | Go asset service |
| `host.window.*`, `host.dialog.*`, `host.clipboard.*`, `host.menu.*` | Go | C++ host |
| `host.event.emit` | Go | C++ host → target renderer frame |
| `host.lifecycle.*` | C++ host | Go application event bus |
| `diagnostic.*` | either | receiver logs/records only |

An unknown operation returns `unsupported_operation`; it must never be silently
ignored. Operation authorization is host-owned: the renderer cannot select a
privileged host operation merely by crafting a Wails payload.

## Response and error shape

```json
{
  "v": 1,
  "kind": "response",
  "capability": "base64url-32-byte-token",
  "id": "same-request-id",
  "ok": false,
  "error": {
    "code": "frame_gone",
    "message": "renderer frame navigated before the response was ready",
    "retryable": false
  }
}
```

Stable error codes are: `unauthenticated`, `protocol_mismatch`,
`payload_too_large`, `invalid_envelope`, `unsupported_operation`, `deadline`,
`cancelled`, `frame_gone`, `backend_unavailable`, `host_unavailable` and
`internal`. Error text is diagnostic only; callers branch on `code`.

## Lifecycle and cancellation

The C++ host indexes every renderer request by `(browserId, frameId, id)`.
Navigation, frame destruction, renderer crash, browser close and sidecar
disconnect immediately remove the entry and issue a best-effort `cancel` to
Go. A late Go response is discarded; it must never be delivered to a new frame
that reused a window or browser identifier.

Go derives a `context.Context` deadline from `deadlineUnixMs`. It cancels work
on `cancel`, socket close or host shutdown. CPU-heavy services must check that
context rather than holding the C++ host thread.

## Compatibility rules

- A sidecar only connects when `v` exactly matches a host-supported version.
- Additive optional fields are allowed within a version; removing or changing
  field meaning requires a new version.
- The C++ host and Go sidecar are one bundle unit and must be released together.
  Cross-version compatibility is deliberately not promised in version 1.
- The current Go-only single-process CEF backend does not use this protocol.

## Verification before implementation

Tests for M0/M3 must cover golden frames, partial reads, bad length prefixes,
invalid UTF-8, unknown fields, capability mismatch, peer UID mismatch, request
flood limits, deadline propagation, cancellation races and late-frame response
discarding. No CEF process is needed for these tests.
