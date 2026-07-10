# cef-hello — minimum-viable CEF opt-in example

A tiny Go-only Wails app that demonstrates the **CEF (Chromium
Embedded Framework) opt-in backend** for Linux.

## Build

```sh
cd v3/examples/cef-hello
go mod tidy
go build -tags cef -o cef-hello .
```

The resulting binary is ~18MB and embeds the HTML shown below.

## Run

```sh
# Install CEF 147 first (see v3/docs/guides/cef.md for one-liners)
CEF_DIR=/usr/lib/cef ./cef-hello
```

Expected behaviour (with libcef 147 installed):

1. A GTK4 application window appears (~800x600).
2. CEF renders the inline HTML.
3. The page fires `window.wails.invoke(...)` which the V8 handler
   routes through the CEF client back to the Wails message
   processor (no-op here, but logs to stderr).

## What this exercises

| Layer | Status |
|---|---|
| Build tag scaffolding (`-tags cef`) | ✅ |
| GTK4 host window via `cefCreateHostWindow` | ✅ |
| `libcef.so` dlopen + version check | ✅ |
| `cef.Init` / `cef.Shutdown` lifecycle | ✅ |
| Asset server `wails://` routing | ✅ |
| `CefFrame::ExecuteJavaScript` (no-op without real DOM) | ✅ |
| Browser-process bridge `cefClientStub.GetRequestHandler` | ✅ |

## Known limitations (documented in IMPLEMENTATION.md)

This example is the **minimum-viable** smoke test. Features that
require additional plumbing are deferred:

- The HTML is inline; no `frontend/` bundle yet.
- No `wails.Call(...)` handler is bound — the JS shim is
  registered but no Go method is exposed.
- DevTools / DnD / permissions are stubbed.
- Body streaming returns 501 in this minimal example (the
  inline HTML is served by the http.HandlerFunc, not via
  `wails://`).

## See also

- `v3/docs/guides/cef.md` — installation, troubleshooting.
- `v3/IMPLEMENTATION.md` — full design notes.
- `v3/history/PLAN.md` — the 6-phase plan this branch implements.