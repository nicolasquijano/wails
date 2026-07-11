# CEF Backend Implementation Tracker

## Overview

This document tracks the CEF (Chromium Embedded Framework) backend for Wails v3 on Linux.

**Current status (2026-07-10)**: The CEF backend is functional for single-process X11 embedding with the Alloy runtime. Multi-process mode and Wayland support are not yet working.

**Build tag**: `cef` (e.g. `go build -tags cef`). Cannot be combined with the WebKit build tags (`gtk3`).

## Architecture Decisions

### Decision C1: Single-process mode required (2026-07-07)

**Context**: CEF multi-process mode spawns subprocesses via Chromium's zygote fork. Go's runtime does not support `fork()` after `runtime.Main()` — threads are not preserved across fork, locks corrupt, goroutine stacks break.

**Decision**: Enforce `--single-process` via `OnBeforeCommandLineProcessing`. Subprocess detection via `ExecuteSubprocessWithApp()` is called but always returns `false` in single-process mode.

**Rationale**:
- `--no-zygote` + multi-process triggers Mojo validation errors (`network.mojom.NetworkContext` deserialization fails) due to incompatible Go ↔ Chromium runtime state
- Single-process is the only reliable mode until the CEF subprocess model can tolerate a Go host
- Tradeoff: no process isolation per renderer, but all major Chromium features work (JS, CSS, WebSocket, Canvas, WebGL)

### Decision C2: X11 only (2026-07-07)

**Context**: CEF's X11 embedding uses `XReparentWindow` to insert the CEF view into a GTK container. This is a pure X11 operation with no Wayland equivalent.

**Decision**: Force GDK backend to X11 (`gdk_set_allowed_backends("x11")`, `unset WAYLAND_DISPLAY`). Pass `--ozone-platform=x11`.

**Rationale**:
- Wayland protocol does not allow arbitrary `XReparentWindow`-style embedding
- GDK Wayland → X11 interop (xdg-foreign) requires CEF to export its surface, which no current CEF build supports
- Switch to Chrome runtime (not Alloy) might enable native Ozone/Wayland in future CEF versions

### Decision C3: No ParentWindow / manual reparenting (2026-07-07)

**Context**: Passing `WindowInfo.ParentWindow = parentXID` to CEF causes `XCreateWindow` to fail with `BadMatch` when the X11 visual of the CEF window (default visual, 24-bit TrueColor) does not match the GTK window's visual (ARGB32).

**Decision**: Do NOT set `ParentWindow`. Let CEF create a top-level window. Reparent it into the GtkBox via `XReparentWindow` in `cef_attach_to_gtk_widget`.

**Rationale**:
- Avoids visual mismatch between GTK and CEF
- CEF creates its window with its own default visual, which is compatible with `XReparentWindow`
- The top-level window briefly appears as a separate window before reparenting (fraction of a second)

### Decision C4: Idle pump for resize tracking (2026-07-10)

**Context**: GtkBox does not fire `notify::width`/`notify::height` reliably during window maximize/tile. GtkWindow fires them before the child box is re-allocated.

**Decision**: Maintain a linked list of `(GtkBox*, XID)` pairs checked on every idle pump tick (`cef_view_list_walk` inside `cef_pump_message_loop_cb`). Only calls `XResizeWindow` when the size actually changes.

**Rationale**:
- Eliminates dependency on GObject property notification timing
- Works for all window manager operations (maximize, tile, unmaximize, drag-resize)
- Negligible overhead — just two `gtk_widget_get_width/height` calls per tick

### Decision C5: Alloy runtime (2026-07-07)

**Context**: CEF 147 defaults to the Chrome runtime. The Chrome runtime does not support `ParentWindow`-style embedding the same way Alloy does.

**Decision**: Force `RuntimeStyle = cef.RuntimeStyleAlloy` in `WindowInfo`.

**Rationale**:
- Alloy is the classic CEF embedding API that supports `XReparentWindow` + `CefBrowserHost::GetWindowHandle`
- Chrome runtime may work better with Ozone/Wayland in the future, but currently breaks reparenting

### Decision C7: Route HTTP POST body through CEF→Go (2026-07-10)

**Context**: The JS runtime uses HTTP fetch POST to `/wails/runtime` by default. The CEF resource handler (`cefResourceRequestHandler`) was creating a Go `http.Request` with `nil` body, discarding the request payload.

**Decision**: Read CEF `Request.GetPostData()` in `Open()` and forward the bytes + selected headers (`x-wails-*`, `content-type`) as the Go request body/headers.

**Rationale**:
- Avoids changing the JS runtime transport or the CEF shim
- The default HTTP fetch path now works end-to-end: `JS fetch → CEF Open() → serveFromAssets() → assetserver → HTTPTransport middleware → HandleRuntimeCallWithIDs()`
- POST body is read synchronously (fine for runtime calls — typically <1KB)

### Decision C6: Custom scheme `wails` with CORS (2026-07-07)

**Context**: The custom `wails://` scheme serves app assets. Initially used `LOCAL | STANDARD | CORS_ENABLED | FETCH_ENABLED | SECURE`.

**Decision**: Remove `LOCAL` and `DISPLAY_ISOLATED` flags. Use `STANDARD | CORS_ENABLED | FETCH_ENABLED | SECURE`.

**Rationale**:
- `LOCAL` causes Chromium to treat the scheme as opaque origin (`null`), which blocks same-origin JS/CSS loading even with CORS headers
- Without `LOCAL`, the origin is `wails://localhost`, JS/CSS loads correctly, and CORS headers in the response are honored

## Implementation Phases

### Phase 1: Core Embedding (✅ COMPLETE)

- [x] CEF library loading via `libcef.so` (purego-cef bindings)
- [x] Runtime discovery (CEF_DIR, /usr/lib/cef, ~/.local/share/cef)
- [x] CEF init with Alloy runtime, single-process, external message pump
- [x] `cefCreateBrowserInWidget` — browser creation + reparenting
- [x] `cefAttachToGTKWidget` — XReparentWindow + tracking list
- [x] Initial window size from `WebviewWindowOptions`
- [x] CEF view resize on idle pump (handles maximize/tile)
- [x] GtkApplicationWindow host window
- [x] Custom `wails://` scheme registration (CORS-safe)
- [x] Resource request handler (Open → GetResponseHeaders → Read)

### Phase 2: Basic App Support (✅ COMPLETE)

- [x] Custom scheme serving local assets
- [x] SPA fallback for client-side routing
- [x] CORS headers for `wails://` origin
- [x] DevTools enabled on port 9999
- [x] `cef-hello` demo — basic HTML/CSS/JS with timer
- [x] `cef-shadcn-admin` demo — shadcn/ui dashboard app with Tailwind
- [x] CDP screenshots work (Page.captureScreenshot)
- [x] JS `setInterval`/`setTimeout` work
- [x] CSS loading (Tailwind classes render)

### Phase 3: IPC & Runtime (🔄 IN PROGRESS)

- [x] V8 handler `wails_invoke` — synchronous Go ← JS calls via `HandleRuntimeCallWithIDs()`
- [x] HTTP POST body + header forwarding from CEF → assetserver (enables the default fetch transport)
- [x] Verified: System.Environment, Window.SetTitle via HTTP fetch work end-to-end
- [ ] V8 extension context isolation (`window.wails.invoke` not accessible from CDP -- low priority, HTTP path works)
- [ ] Async callback resolution (`wails_callback` — Go → JS push notifications, Promise resolve)
- [ ] Event emission (`wails_emit` — Go → JS event dispatching)
- [ ] Window management from JS (resize, close, minimize, maximize)
- [ ] Application lifecycle hooks
- [ ] Drag & drop file events
- [ ] System dialogs (file open/save, alerts)

### Phase 4: Features & Polish (📋 PENDING)

- [ ] Right-click context menu handler
- [ ] DevTools hotkey (F12)
- [ ] CEF auto-download runtime installer
- [ ] Multi-process mode (resolve Mojo validation errors)
- [ ] Window icon via CEF
- [ ] Print handler

### Phase 5: Wayland (📋 PENDING)

- [ ] Investigate Chrome runtime Ozone/Wayland support
- [ ] CEF surface export via xdg-foreign
- [ ] Test with GDK_BACKEND=wayland
- [ ] Window positioning NO-OP on Wayland (Decision 3)

## Known Issues

### Critical
- **Multi-process crashes**: Mozilla validation errors in `network.mojom.NetworkContext.1` deserialization when using `--no-zygote` without `--single-process`. Root cause: Go runtime state incompatible with Chromium subprocess fork.

### Medium
- **No Wayland**: `XReparentWindow` is X11-only. Full Wayland support requires Chrome runtime + Ozone.
- **V8 Proxy resolver warning**: "Cannot use V8 Proxy resolver in single process mode." Not harmful but logged every startup.
- **nvidia-drm warning**: `Should skip nVidia device named: nvidia-drm`. Not harmful.
- **No user type filter warning**: For CEF built-in extension `cimiefiiaegbelhefglklhhakcgmhkai`. Not harmful.

### Low
- **Deprecated X11 GDK functions**: `gdk_x11_display_get_xdisplay`, `gdk_x11_surface_get_xid` trigger compiler warnings. GTK4 marks them deprecated but no replacement exists for XReparentWindow workflow.

## Key Files

| File | Role |
|------|------|
| `v3/pkg/application/linux_cgo_cef.go` | CGo C code: X11 embedding, reparenting, idle pump, view tracking list |
| `v3/pkg/application/cef_app_stub.go` | CefApp callbacks (OnRegisterCustomSchemes, OnBeforeCommandLineProcessing) |
| `v3/pkg/application/cef_request_handler.go` | Resource request handler (wails:// scheme serving) |
| `v3/pkg/application/webview_window_linux_cef.go` | Go-level window creation, init sequence |
| `v3/examples/cef-hello/main.go` | Minimal demo: card UI with gradient, timer, CDP test |
| `v3/examples/cef-shadcn-admin/main.go` | Full demo: shadcn/ui dashboard with SPA routing |

## Runtime Dependencies

- `libcef.so` (with debug_info) at `~/.local/share/cef/`
- `Resources/` directory with `.pak` files, `locales/` (220 locale files)
- `libvk_swiftshader.so`, `libEGL.so`, `libGLESv2.so` in runtime dir
- X11 display (`DISPLAY`, `GDK_BACKEND=x11`)
- GTK4, libX11

## Commits (feat/linux-cef branch)

| Date | Hash | Description |
|------|------|-------------|
| 2026-07-07 | 947877e5a | feat(v3/cef): add cef-shadcn-admin demo, fix CORS opaque origin |
| 2026-07-08 | ede680b2c | fix(v3/cef): set CEF Bounds to actual window size, not hardcoded 800x600 |
| 2026-07-10 | 894ec0174 | fix(v3/cef): connect resize signal to GtkWindow, not GtkBox |
| 2026-07-10 | 1cd26cde2 | fix(v3/cef): defer resize via idle callback to catch post-layout size |
| 2026-07-10 | 06f969936 | fix(v3/cef): track CEF views via linked list and resize on idle pump |
| 2026-07-10 | 30ccd0981 | feat(v3/cef): forward POST body and headers from CEF to assetserver |
