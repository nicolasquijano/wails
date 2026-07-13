# CEF Backend Implementation Tracker

## Overview

This document tracks the CEF (Chromium Embedded Framework) backend for Wails v3 on Linux.

**Current status (2026-07-13)**: Single-process CEF backend complete (Phases 1-6). Decision C18 (multi-process architecture) implementation in progress — M1 (native C++ host skeleton) and M2 (Go sidecar) started.

**Multi-process status (Decision C18)**: Native C++ `wails-cef-host` skeleton created with CMake build, CEF init, GTK/X11 window embedding. Go `wails-go-runtime` sidecar created with Unix socket RPC framing. M3 (secure transport) pending.

**Wayland status (2026-07-12)**: Wayland support reverted to X11-only. CEF 147's Wayland backend is not production-ready. All CEF builds force `GDK_BACKEND=x11` and `--ozone-platform=x11`, relying on XWayland on Wayland sessions. See Decision C16.

**Build tag**: `cef` (e.g. `go build -tags cef`). Cannot be combined with the WebKit build tags (`gtk3`).

## Architecture Decisions

### Decision C1: Single-process mode required (2026-07-07)

**Context**: CEF multi-process mode spawns subprocesses via Chromium's zygote fork. Go's runtime does not support `fork()` after `runtime.Main()` — threads are not preserved across fork, locks corrupt, goroutine stacks break.

**Decision**: Enforce `--single-process` via `OnBeforeCommandLineProcessing`. Subprocess detection via `ExecuteSubprocessWithApp()` is called but always returns `false` in single-process mode.

**Rationale**:
- `--no-zygote` + multi-process triggers Mojo validation errors (`network.mojom.NetworkContext` deserialization fails) due to incompatible Go ↔ Chromium runtime state
- Single-process is the only reliable mode until the CEF subprocess model can tolerate a Go host
- Tradeoff: no process isolation per renderer, but all major Chromium features work (JS, CSS, WebSocket, Canvas, WebGL)

### Decision C2: X11 only (2026-07-07) — SUPERSEDED by C15 (2026-07-11)

**Context**: CEF's X11 embedding uses `XReparentWindow` to insert the CEF view into a GTK container. This is a pure X11 operation with no Wayland equivalent.

**Original decision (C2)**: Force GDK backend to X11 (`gdk_set_allowed_backends("x11")`, `unset WAYLAND_DISPLAY`). Pass `--ozone-platform=x11`.

**Superseded by Decision C15 (Phase 5)**: Wayland is now supported via the Chrome runtime + Ozone/Wayland. On Wayland sessions CEF runs as its own top-level Wayland window (`cefCreateBrowserDetached`); the GTK4 host stays as a placeholder. See Decision C15 for the full rationale. The X11-only paths (`XReparentWindow`, `gdk_set_allowed_backends("x11")`, `gdk_x11_surface_get_xid`) remain in place for X11 sessions — they're gated behind `isOnWayland()` so they're never called on Wayland.

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

### Decision C8: OnLoadEnd drives runtime-ready instead of JS invoke (2026-07-10)

**Context**: The CEF V8 extension (`cef_js_shim.js`) registers a native `wails_invoke` function and sets `window.wails.invoke = function(msg) { wails_invoke(msg); }` long before any page script runs. However, the Wails runtime JS module (`runtime.js`, line 1622) later assigns `window.wails = index_exports`, **overwriting the entire `window.wails` object** and losing the native `invoke` function. Consequently, `invoke("wails:runtime:ready")` silently fails, `runtimeLoaded` stays `false`, and all pending JS (`ExecJS` calls before ready) stays queued forever.

**Decision**: Hook CEF's `OnLoadEnd` callback (on `cef.LoadHandler`) and call `WebviewWindow.HandleMessage("wails:runtime:ready")` from Go when the main frame finishes loading. The callback is dispatched via `InvokeAsync` to avoid deadlocking on CEF's UI thread (which is the main thread in single-process mode).

**Rationale**:
- The Go-side `OnLoadEnd` fires after all deferred scripts (including `runtime.js`) have executed — `window._wails.dispatchWailsEvent` is guaranteed to be available
- Avoids fighting JS module-level assignment order
- No changes needed to the compiled runtime bundle
- Works even when the JS-side "Browser Environment Detected" warning is shown (because `_invoke` is null)

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

### Decision C9: X11 helpers for window management (2026-07-11)

**Context**: GTK4 removed `gtk_window_move`, `gtk_window_get_position`, `gtk_window_set_keep_above`, and `WM_NORMAL_HINTS`-based max-size hints. The WebKit/GTK4 backend has these as static C functions in `linux_cgo.c` (tagged `!cef`).

**Decision**: Add C helpers directly in the CGo preamble of `linux_cgo_cef.go`:
- `window_move_x11` — X11 `XMoveWindow` (no GTK4 equivalent)
- `window_get_position_x11` — X11 `XTranslateCoordinates`
- `window_set_always_on_top_x11` — `_NET_WM_STATE` X11 atom
- `window_set_max_size_x11` — `WM_NORMAL_HINTS`
- `window_is_minimised` — `gdk_toplevel_get_state`

**Rationale**:
- The CEF backend links `x11` directly via `#cgo pkg-config: x11`, so direct Xlib calls work without `dlsym` indirection
- Functions are static helpers inside the CGo preamble — no need for a separate `linux_cgo_cef.c` file
- Same approach used by the WebKit backend (just bundled in the same file as the GTK4 helpers there)

### Decision C10: System dialogs use Chromium defaults (2026-07-11)

**Context**: CEF provides three integration points for dialogs:
- `cef.DialogHandler.OnFileDialog` — called when renderer requests a file dialog (e.g. `<input type="file">`)
- `cef.JsdialogHandler.OnJsdialog` — called for `alert`/`confirm`/`prompt`
- `CefBrowserHost::RunFileDialog` — Go-initiated file dialog

In single-process mode (Decision C1), `RunFileDialog` triggers SIGSEGV when the dialog is dismissed. The crash is in Chromium's V8/Mojo teardown path that requires subprocess isolation.

**Decision**: 
- `cefDialogHandler.OnFileDialog` returns 0 → Chromium uses its native chooser
- `cefJsdialogHandler.OnJsdialog` returns 0 → Chromium uses native modal dialogs
- `cefJsdialogHandler.OnBeforeUnloadDialog` always returns true → no JS confirm prompt on window close
- `linuxOpenFileDialog.show` / `linuxSaveFileDialog.show` return graceful error (not crash)

**Rationale**:
- Renderer-initiated `<input type="file">` works perfectly via return-0
- JS `alert`/`confirm`/`prompt` work via return-0 (Chromium renders modals)
- Go-initiated file dialogs are blocked at the CEF level, not our bug — would need a separate subprocess binary to fix (Decision C1)
- Wails apps use JS-side modals for messages, not Go MessageDialog, so the no-op there is fine

### Decision C11: Drag-and-drop via DragHandler.OnDragEnter + native bridge (2026-07-11)

**Context**: The Wails runtime JS (`runtime.js`) attaches `dragenter`/`dragover`/`drop` listeners to `documentElement` and, on drop, recovers file paths via `window.chrome.webview.postMessageWithAdditionalObjects` — a WebView2-only API. CEF has no equivalent. Additionally, the standard browser `dataTransfer.files` exposes `File` objects (which carry content but no local path), not the absolute paths the Wails `WindowFilesDropped` event expects.

CEF does, however, give us `cef.DragHandler.OnDragEnter`, which fires *before* the DOM drop event with a `cef.DragData` whose `GetFilePaths()` returns the OS-level absolute paths of the files being dragged.

**Decision**: Wire `cef_drag_handler.go` so that:

1. `OnDragEnter` stashes the `DragData` on the window's `dragSlot` and returns `DragOperationCopy`. Returning `Copy` (instead of `None`) keeps the drag alive so the browser still fires DOM drop events.
2. A new native function `wails_cefResolveDrop(id, x, y)` is added to `cef_js_shim.js` and routed through `cef_v8_handler.go::handleCefResolveDrop`. The id argument is the CEF browser identifier, which Go exposes to JS via `OnDocumentAvailableInMainFrame` (`window._wailsCefBrowserId`).
3. The JS shim installs a capture-phase `drop` listener that calls `wails_cefResolveDrop`, parses the JSON array of paths, and forwards them to `_wails.handlePlatformFileDrop(paths, x, y)`. Capture phase guarantees we run *before* the runtime's bubble-phase listener that depends on WebView2.
4. `cefCreateBrowserInWidget` registers `registerCefBrowser(id, w)` so the V8 handler can find the owning window from the browser id. `destroy()` calls `unregisterCefBrowser(id)` to drop the mapping on shutdown.

**Rationale**:
- The WebView2 path in `runtime.js` keeps working unchanged for Windows; CEF gets its own path through the new shim function.
- Per-window `dragSlot` (mutex-protected) handles concurrent multi-window drags without races.
- The `dragData` reference is only safe to read during an active drag; clearing it inside `cefResolveDragDrop` (under lock) means a stale drop handler can't see leaked paths.
- `intToJSNumber` is a deterministic int32→JS literal formatter used to inject `window._wailsCefBrowserId` without pulling `strconv` into the JS-injection hot path.

### Decision C12: Async transport via `wails_invokeAsync` + `_wailsAndroidCallback` (2026-07-11)

**Context**: The CEF V8 extension currently exposes only `wails_invoke` (synchronous — the JS thread blocks until Go returns). Long-running service methods (anything taking >100ms, network calls, file I/O, etc.) freeze the UI because V8 and the GTK main loop are the same thread in single-process mode. The Wails runtime JS already supports an async transport on Android (`window.wails.invokeAsync` + `_wailsAndroidCallback`) — when present, `runtimeCallWithID` uses a customTransport that returns a Promise immediately, lets Go process the call in the background, and resolves the Promise when Go injects `_wailsAndroidCallback(id, response, error)` via the webview's evaluateJavascript.

**Decision**: Wire the same pattern for CEF:

1. **JS shim** (`cef_js_shim.js`) adds a new native binding `wails_invokeAsync(callId, payload)` and exposes `window.wails.invokeAsync = (callId, payload) => wails_invokeAsync(...)`. When the runtime detects `window.wails.invokeAsync` is a function, it auto-switches to customTransport — no change to the runtime bundle required.

2. **Go side** (`cef_v8_handler.go`):
   - `handleInvokeAsync(browserId, callId, payload)` spawns a goroutine that:
     - Decodes the JSON RuntimeRequest.
     - Calls `cefV8Proc.HandleRuntimeCallWithIDs(ctx, &req)` (the same path as sync, with the same 30s timeout).
     - On completion calls `cefResolveAsyncCall(w, callId, payload, err)` which schedules `frame.ExecuteJavaScript("window._wailsAndroidCallback(...)")` on the CEF UI thread via `InvokeAsync` (g_idle_add_full in the CGo preamble).
   - `cefResolveAsyncCall` builds the success envelope `{ok: true, data: <json>}` or the error variant; the runtime's `_wailsAndroidCallback` looks up the Promise in its `pending` map and resolves/rejects it.

3. **`wails_callback` round-trip** is also wired: `window.wails.handleCallback(id, ok, result)` (defined in the JS shim) calls native `wails_callback`, which records into a bounded ring (`cefCallbackLog`, cap 64) for future push-notification subscribers. Today this is a no-op for delivery — the round-trip exists so future Go→JS→Go flows (event confirmations, subscription ACKs) don't need a shim change.

**Rationale**:
- Blocking V8 inside a service method freezes the entire UI (window can't be moved, button clicks don't register). The async transport fixes this without changing the MessageProcessor or service method signatures.
- `window.wails.invokeAsync` being a function is the runtime's **only** trigger for switching to customTransport; no build step, no runtime bundle update.
- The success envelope `{"ok":true,"data":...}` mirrors the runtime's existing JSON.parse logic exactly, so the JS Promise resolves to the same value the sync path returns.
- `InvokeAsync` is required for the JS injection — `frame.ExecuteJavaScript` is only safe on the CEF UI thread (which is the main thread in single-process mode). Running it from the goroutine would race with V8 and the Chromium message loop.
- The bounded callback ring is for diagnostics; once a real push-notification consumer exists it should switch to a map keyed by call id.

### Decision C14: V8 extension context isolation (2026-07-11)

**Context**: The CEF V8 extension shim (`cef_js_shim.js`) defined `wails_invoke` as a `native function` inside an IIFE and exposed `window.wails.invoke` as a thin wrapper. Page scripts — including any DevTools user — could call the wrapper freely. The native function itself was hidden by V8's lexical-scope rule on native bindings, but the wrapper had no defense-in-depth: any XSS payload or DevTools console could call `Call.ByName("main.Greeter.SensitiveOp", ...)` without restriction.

**Decision**: Layer three defenses on top of the existing closure isolation:

1. **Closure isolation (already in place, now documented)**: V8 enforces lexical scope on `native function` declarations (per the CEF docs: "The calling of a native function is restricted to the scope in which the prototype of the native function is defined"). All eight native bindings (`wails_invoke`, `wails_invokeAsync`, `wails_callback`, `wails_log`, `wails_setFlags`, `wails_setEnvironment`, `wails_emit`, `wails_cefResolveDrop`) live inside the IIFE. The only attack surface from page scripts is the JS wrappers on `window.wails` / `window._wails`.

2. **Input validation in the wrappers** (`cef_js_shim.js`): `wailsInvoke` and `wailsInvokeAsync` now validate argument shape (rejects `null` / `undefined` payloads, wraps objects via `JSON.stringify`, wraps thrown native errors into `console.error` rather than letting them propagate as uncaught exceptions). Both wrappers `try/catch` around the native call so a Go-side panic can't surface as an uncaught JS exception.

3. **Optional method allow-list** (`Options.Security.AllowedMethods`): when non-empty, the wrapper rejects `Call.ByName(...)` invocations whose `methodName` isn't in the list. Built-in subsystems (`Window.*`, `Events.*`, `System.*`, `Screens.*`, `Clipboard.*`, `Browser.*`, `Dialogs.*`, `Application.*`, `CancelCall.*`, `IOS.*`, `Android.*`) are always allowed because the runtime uses them internally — locking them out would break the runtime. Default = no restriction (matches prior behavior).

4. **Optional per-navigation CSRF nonce** (`Options.Security.EnforceCSRFNonce`): when true, every `window.wails.invoke(msg, nonce)` / `window.wails.invokeAsync(id, payload, nonce)` must carry a nonce matching `window._wails.cefNonce` (a 16-char hex string Go regenerates on every `OnDocumentAvailableInMainFrame` via `crypto/rand`). The runtime bundle doesn't pass a nonce — that's fine when enforcement is off; when it's on, every runtime call from the runtime bundle is rejected, which is the intended behavior for hardening deployments where you want only *your* code calling IPC, not the runtime's existing `Call.ByName` plumbing. Apps that opt in have to wrap their own JS to read the nonce and pass it on every call.

**Threat model**:

| Layer | What it blocks | What it doesn't block |
|---|---|---|
| Native lexical scope | Direct calls to `wails_invoke` etc. from outside the IIFE | Wrapper calls (intentional) |
| Wrapper input validation | Null/non-serializable payloads, uncaught exceptions | A motivated DevTools user |
| Method allow-list | Service methods (`main.Greeter.X`) not in the list | Built-in subsystem methods (Window, Events, etc.) |
| CSRF nonce | Runtime bundle's default `Call.ByName` calls (when enforced) | Any JS the attacker controls on the page |

A DevTools user can always read `window._wails.cefNonce` and pass it on; the nonce only defeats scripted IPC that runs without page-script execution (e.g., a future hypothetical "blind" IPC that the runtime bundle does on its own). The allow-list is the meaningful defense: it bounds the *set* of methods that can be called regardless of how the wrapper is invoked.

**Implementation notes**:

- `Options.Security` is a new struct in `application_options.go`. All fields are opt-in (zero value = prior behavior) and the field is a no-op on non-CEF builds.
- `setCefSecurityOptions(app)` caches the allow-list + enforcement flag in `cefSecurityCache` (RWMutex-protected, set once at app init).
- `buildCefSecurityInjection(nonce)` returns a JS fragment appended to `OnDocumentAvailableInMainFrame`'s `flags`/`environment` push. It sets `window._wails.cefAllowedMethods`, `cefEnforceNonce`, `cefNonce` on every navigation.
- `generateCefCSPNonce` reads 8 bytes from `crypto/rand` and hex-encodes them (16 lowercase hex chars). Smoke-tested with `TestGenerateCefCSPNonceSmoke` (variation + format).
- `appendJSQuoted` handles the C0/quote/backslash escapes a random nonce might need (tested with `TestAppendJSQuoted`).
- The wrapper uses `indexOf` (not `Array.includes`) on the JSON array literal because V8's Array.prototype.includes wasn't always available across the Chromium versions we support; `indexOf` is universally available.

### Decision C15: Wayland support (Phase 5) — SUPERSEDED by C16 (2026-07-12)

**Context**: Phase 5 attempted native Wayland support. CEF 147 ships with Ozone/Wayland in `libcef.so` but the Alloy runtime has no Wayland surface implementation. The Chrome runtime + Ozone/Wayland path was prototyped but produced a detached CEF window (not embedded in the GTK4 host).

**Decision (original, now superseded)**: 
1. Detect Wayland sessions via union of `WAYLAND_DISPLAY`, `XDG_SESSION_TYPE`, `GDK_BACKEND`.
2. Switch CEF to Chrome runtime + Ozone/Wayland on Wayland.
3. Create browser detached (`cefCreateBrowserDetached`) — CEF owns its own top-level Wayland surface.
4. Window positioning becomes a no-op on Wayland.

**Superseded by Decision C16**: Wayland is not production-ready; reverted to X11-only.

### Decision C16: X11-only — Wayland dropped (2026-07-12)

**Context**: Phase 5 (Decision C15) added Wayland support via Chrome runtime + Ozone/Wayland with detached browser windows. Testing revealed two critical problems:

1. **Separate window, not embedded**: CEF creates its own top-level Wayland surface via Ozone/Wayland. The GTK4 host window is a placeholder — the CEF view is a separate window the user must find and interact with independently. The xdg-foreign protocol (required to embed one Wayland surface into another) is not exposed by CEF 147's Chrome runtime.

2. **No upstream framework ships it**: Electron does not ship Ozone/Wayland by default (requires `--ozone-platform-hint=auto` and still has known issues). Energy framework explicitly states "Currently, under Linux, only x11 can be used, and Wayland cannot be used yet." No production desktop framework uses CEF on native Wayland.

3. **V8 startup snapshot on Wayland**: CEF 147 on Wayland requires `v8_context_snapshot.bin` in the resources directory (`Resources/`). The build-tree layout of our CEF 147 installation doesn't place it there by default — manual copy required. This was fixed by copying the file from `Release/` to `Resources/`.

**Decision**: Revert to X11-only for all CEF builds. On Wayland sessions, force `GDK_BACKEND=x11` and `--ozone-platform=x11` so GTK+CEF operate through XWayland. The changes:

| File | Change |
|------|--------|
| `application_linux_cef.go::init()` | Always force `GDK_BACKEND=x11`, `OZONE_PLATFORM=x11`, unset `WAYLAND_DISPLAY` |
| `linux_cgo_cef.go::cefInit()` | Always call `gdk_set_allowed_backends("x11")` before `gtk_init()` |
| `linux_cgo_cef.go::cefCreateBrowserInWidget()` | Remove Wayland branch; always use X11 reparenting path |
| `linux_cgo_cef.go::cefMoveWindow()` | Remove Wayland short-circuit |
| `cef_app_stub.go::OnBeforeCommandLineProcessing` | Remove Wayland branch; always `--ozone-platform=x11 --runtime-style=alloy` |
| `webview_window_linux_cef.go::move()` | Remove Wayland short-circuit |

**Rationale**:
- `XReparentWindow` embedding works reliably under XWayland on any modern Wayland compositor (KDE, GNOME, Hyprland, Sway).
- The user's session has `DISPLAY=:1` (XWayland) available, which is sufficient.
- The detached-window approach (Phase 5) produced a blank/unresponsive GTK host window; embedding via XWayland produces a single window with correct CEF content.
- CEF 147's Wayland Ozone backend may mature in future versions; this decision can be revisited when an upstream framework demonstrates working CEF+Wayland in production.

**File requirements on disk** (beyond libcef.so):
```
cef_dir/
├── icudtl.dat                          # next to libcef.so  (FATAL without)
├── v8_context_snapshot.bin             # in Resources/       (FATAL without)
├── chrome_100_percent.pak              # in Resources/
├── chrome_200_percent.pak              # in Resources/
├── resources.pak                       # in Resources/
├── locales/en-US.pak (and others)      # in Resources/locales/
└── libEGL.so, libGLESv2.so            # in Release/ (used via LD_LIBRARY_PATH)
```

The ICU data file (`icudtl.dat`) must be present next to `libcef.so` (CEF issue #3778). The V8 context snapshot (`v8_context_snapshot.bin`) must be in the resources directory resolved by `PathService::Get(5)` — when `resources_dir_path` is set in `CefSettings`, this is the configured path.

### Decision C17: C++ subprocess helper alone is insufficient (2026-07-12)

**Experiment**: A C++20 `wails-cef-helper` was built against the same CEF 147
distribution and configured through `browser_subprocess_path`. The graphical
smoke test confirmed that zygote and network utility processes did execute the
native helper, not the Go executable.

**Result**: Rejected as a complete solution. The Go browser process still
launches Chromium children. With the helper in use, CEF repeatedly emitted
`VALIDATION_ERROR_DESERIALIZATION_FAILED` for
`network.mojom.NetworkContext` and `content.mojom.NavigationClient`.
Consequently, replacing only renderer/zygote executables does not make a Go
browser host safe for Chromium multi-process IPC.

The experiment remains opt-in (`WAILS_CEF_MULTIPROCESS=1`) and must not be
enabled for users. The single-process fallback remains the supported backend.

### Decision C18: Native C++ CEF host with Go runtime backend (🔄 IN PROGRESS 2026-07-13)

**Decision**: The viable multi-process architecture moves ownership of the
*entire CEF browser process* to a native C++ executable. Go remains the Wails
runtime and application-service implementation, but runs as a separate backend
process; it never initializes CEF or owns GTK/Chromium windows.

```text
frontend JavaScript
        | CEF V8 extension / CefProcessMessage
        v
wails-cef-host (C++ browser process)
        | owns CefInitialize, GTK windows, zygote, renderer, GPU, utility
        | authenticated Unix-domain RPC
        v
wails-go-runtime (separate Go executable)
        | MessageProcessor, bindings, assets, events, services, SQLite, RAW
```

**Why this is viable**: Chromium's browser process and every process that it
may fork are native CEF code. Go starts only as an ordinary child service of the
host (via exec/`posix_spawn`) and never appears in Chromium's zygote lineage.
This is analogous to Electron's model: Electron owns Chromium's browser process
in its native runtime and exposes a language runtime through controlled IPC;
it is not a generic Node executable serving as CEF's host.

**Implementation plan**:

1. **C++ host bootstrap** — create `wails-cef-host` using the CEF 147 SDK. It
   calls `CefExecuteProcess` before `CefInitialize`, owns `CefApp`, custom
   schemes, GTK/X11 host windows, message pumping, lifecycle and packaging of
   all CEF resources. It uses normal multi-process CEF switches; no
   `single-process`, `in-process-gpu`, or `--no-zygote` workaround.
2. **Go backend executable** — split the current Go platform-independent
   runtime/services from `pkg/application` into a process started by the C++
   host. It receives the application configuration and a private socket path,
   but imports no CEF/GTK code. Existing service APIs, `MessageProcessor`,
   SQLite, file access and RAW work remain Go.
3. **Authenticated local RPC** — use a per-launch Unix-domain socket in a
   `0700` runtime directory plus a random capability token passed only to the
   child Go process. Define versioned request/response envelopes containing
   request ID, window/frame identity, operation, payload, deadline and
   cancellation. Reject unknown versions, stale frames, oversized payloads and
   unauthenticated peers.
4. **Renderer bridge** — the C++ render-process handler registers
   `wails.cef`. Native V8 functions send `CefProcessMessage` requests to the
   C++ browser process; it forwards them asynchronously to Go RPC and returns
   the result to the originating frame. Keep Wails' Promise transport as the
   default; synchronous binding calls are compatibility-only.
5. **Host APIs and assets** — window, menus, dialogs, clipboard, drag/drop and
   event delivery become explicit C++ host RPC operations. The C++ resource
   handler proxies `wails://` asset/runtime requests to Go over the same
   authenticated channel (or a dedicated read-only asset channel); no renderer
   obtains direct access to the Go service socket.
6. **Migration and packaging** — ship `wails-cef-host`, `wails-go-runtime` and
   one pinned CEF distribution together. Preserve the current Go-only
   single-process backend as a selectable fallback until feature parity is
   verified. Do not load Go as a shared library inside the host or helper.

#### Execution plan (C++ host)

| Milestone | Deliverable | Boundary and acceptance criterion |
|---|---|---|
| M0 — Contract freeze (🔄 DRAFTED) | [`v3/docs/cef-host-protocol.md`](v3/docs/cef-host-protocol.md) plus versioned wire schema | Defines maximum payload, request IDs, deadline/cancel semantics, window/frame identity, error envelope, capability token and allowed host operations. No CEF or Go code changes before this contract is reviewed. |
| M1 — Native host skeleton | `v3/pkg/application/cef_host/` CMake project and `wails-cef-host` | A C++20 executable calls `CefExecuteProcess` before `CefInitialize`, creates one GTK/X11 window and loads a static `wails://` page using normal renderer/GPU/utility processes. The process tree must contain no Go ancestor of zygote. |
| M2 — Go runtime sidecar | `cmd/wails-go-runtime/` with Unix-socket listener | The C++ host starts Go with `posix_spawn`/exec, waits for a ready handshake with timeout, and kills/reaps it on host shutdown. The Go process imports platform-neutral Wails runtime code only; CEF/GTK imports are compile-time forbidden. |
| M3 — Secure transport | Framed Unix-domain RPC implementation in both processes | Socket directory is `0700`, socket is owner-only, every frame carries protocol version + launch capability, and input/output sizes are capped before allocation. Unit tests cover forged token, malformed length, unsupported version, timeout and peer disconnect. |
| M4 — Asset and startup path | Go asset service plus C++ `ResourceRequestHandler` proxy | `wails://localhost/` loads the current runtime/assets through the private RPC channel. Browser startup, SPA fallback, MIME types and HTTP POST runtime payloads match the current `cef_request_handler.go` behaviour. |
| M5 — Async bindings | Renderer V8 extension → C++ host → Go `MessageProcessor` | `wails.Call.ByName`, cancellation and Go→JS events work with concurrent requests. Responses are addressed to their original browser/frame and dropped after navigation/close. The existing synchronous V8 path is not used in the new backend. |
| M6 — Native feature parity | C++ host RPC adapters for window, dialogs, menus, clipboard, DnD and lifecycle | Each public Wails API is either implemented, explicitly unsupported with a stable error, or retained behind the single-process fallback. File dialogs, renderer crash/reload and multi-window lifecycle are mandatory before promotion. |
| M7 — Build and package | One build command produces host, Go sidecar and pinned CEF bundle | Host has `$ORIGIN` runtime lookup, validates CEF data files at startup, preserves executable permissions, and runs from a clean bundle without `CEF_DIR`, source tree or developer SDK. |
| M8 — Promotion | Feature flag defaults to multi-process | Only after all parity and fault-injection checks pass on X11/XWayland. Single-process remains available for one release cycle as an emergency fallback, then its removal is a separate decision. |

**C++ host responsibilities**:

- Own `CefApp`, `CefClient`, renderer-process handler, CEF custom scheme,
  request handler and all `CefBrowser`/`CefFrame` references.
- Own GTK/X11 window creation, reparenting, resize/focus signals, native menus,
  dialogs, drag/drop and CEF message-loop integration.
- Maintain a bounded pending-request map keyed by `(browser_id, frame_id,
  request_id)`; clear it on frame detach, renderer termination, navigation and
  backend disconnect.
- Never deserialize business payloads into C++ domain types. It validates the
  transport envelope then forwards opaque JSON/bytes to Go.

**Go sidecar responsibilities**:

- Own `MessageProcessor`, service binding registry, asset server, event bus,
  application configuration, data access, files and all CPU-intensive work.
- Return only serializable response/event envelopes; it never holds a GTK,
  CEF, browser or frame pointer.
- Treat the host connection as cancellable: when it closes, cancel outstanding
  request contexts and exit cleanly rather than attempting to restart CEF.

**Migration order and compatibility**:

1. Land M0–M3 without changing the default CEF backend.
2. Prove M4–M5 in a new `cef-host-hello` example before moving existing demos.
3. Migrate `cef-hello`, then `cef-multiwin`, then `cef-shadcn-admin`; compare
   each against the current verification matrix.
4. Migrate host APIs in groups: lifecycle/window state → events/bindings →
   assets/network → dialogs/clipboard/DnD → printing/devtools.
5. Remove the opt-in Go-browser helper experiment only after M1 is proven; it
   must not become a second production multi-process path.

**Fault-injection and performance requirements**:

- Kill renderer, GPU, Go sidecar and host independently; verify ownership,
  cleanup, visible error and recovery policy for each case.
- Apply request floods, 16 MB oversized frames, malformed JSON, closed-frame
  replies, reconnect attempts and delayed backend responses.
- Measure launch-to-first-paint, p50/p95 binding round-trip, renderer memory,
  host memory, Go memory and GPU/WebGL availability against the current
  single-process baseline. UI input must not wait for Go RPC; expensive work is
  always asynchronous and cancellable.

**Validation gates**:

- A process-tree test proves that the browser, zygote, renderer, GPU and
  network utility processes are all descendants of `wails-cef-host`, while Go
  is a sibling child service rather than an ancestor.
- `cef-hello` loads assets, executes bound methods, resolves concurrent async
  calls, emits Go→JS events, supports multi-window lifecycle and rejects stale
  responses after navigation/close.
- Renderer crash/reload does not terminate Go; Go restart produces a clear
  renderer-visible error and can recover without corrupting the host.
- Canvas/WebGL, GPU process, native file dialog, drag/drop and X11/XWayland
  window operations pass graphical smoke tests using the packaged runtime.
- Unix-socket permissions, token authentication, malformed messages, timeout,
  cancellation, shutdown and child-process reaping have automated coverage.

**Status**: 🔄 IN PROGRESS — M1 (native host skeleton) and M2 (Go sidecar) started 2026-07-13.

## Implementation Progress (Decision C18)

### M0 — Contract freeze ✅ COMPLETE (2026-07-12)
- Protocol defined in `v3/docs/cef-host-protocol.md`
- Version 1 envelope schema with 7 kinds: hello, ready, request, response, event, cancel, shutdown
- Framed transport: 4-byte big-endian length + UTF-8 JSON
- Max payload: 16 MiB, max concurrent requests: 256

### M1 — Native host skeleton 🔄 IN PROGRESS
**Files created**:
```
v3/cmd/wails-cef-host/
├── CMakeLists.txt           # C++20, links CEF + GTK4 + X11
├── include/
│   ├── host_app.h           # cefApp + cefBrowserProcessHandler
│   ├── window_host.h       # GTK/X11 window host wrapper
│   └── ipc_handler.h       # Unix socket RPC primitives
└── src/
    ├── main.cc             # main: CefExecuteProcess + gtk_main
    ├── host_app.cc         # CefApp impl, command-line switches
    └── window_host.cc      # GTK socket embedding, resize handling
```

### M2 — Go runtime sidecar 🔄 IN PROGRESS
**Files created**:
```
v3/cmd/wails-go-runtime/
├── go.mod                  # module github.com/wailsapp/wails/v3/cmd/wails-go-runtime
└── main.go                 # Unix socket client, MessageProcessor bridge, envelope framing
```

### M3 — Secure transport 📋 PENDING
- Unix socket server in C++ (listening)
- Go client connects with hello/ready handshake
- Capability token validation on every frame

### M4 — Asset and startup path 📋 PENDING
- C++ ResourceRequestHandler proxies `wails://` to Go asset service

### M5 — Async bindings 📋 PENDING
- V8 extension → C++ host → Go MessageProcessor over RPC

### M6 — Native feature parity 📋 PENDING
- window, dialogs, menus, clipboard, DnD

### M7 — Build and package 📋 PENDING
### M8 — Promotion 📋 PENDING

### Decision C13: Application lifecycle hooks for CEF (2026-07-11)

**Context**: The CEF backend was missing the standard Linux lifecycle event surface: `events.Linux.ApplicationStartup` (→ `Common.ApplicationStarted`), `SystemWillSleep`/`SystemDidWake` (sleep/wake), `SystemThemeChanged` (→ `Common.ThemeChanged`), and per-window `WindowLoadStarted`/`WindowLoadFinished`/`WindowFocusIn`/`WindowFocusOut`. Apps subscribing to `events.Common.*` got nothing on CEF builds because:

1. `events_common_linux.go` is `//go:build linux && !cef && !android && !server` — excluded from CEF.
2. `application_linux_dbus.go` (logind power events, xdg portal theme) is the same tag.
3. The `linuxApp.run()` method in `application_linux_cef.go` was a 4-line Phase-1 stub that never emitted `ApplicationStartup`.
4. `listenForSystemThemeChangesCEF` existed but was an orphan function never invoked.
5. `cef_load_handler.go` fired only `HandleMessage("wails:runtime:ready")` — no `WindowLoadFinished`.
6. CEF itself has no focus signal; we needed a `GtkEventControllerFocus` like the WebKit backend's.

**Decision**: Add a CEF-specific lifecycle file (`events_common_linux_cef.go`) that mirrors the WebKit backend's `events_common_linux.go` but selected via `//go:build linux && cgo && cef && !android && !server`. It exports:

- `commonApplicationEventMapCEF` — the same Linux→Common event map the WebKit backend uses (ApplicationStartup→ApplicationStarted, SystemThemeChanged→ThemeChanged, SystemWillSleep, SystemDidWake). Verified by `TestCommonApplicationEventMapCEFCoverage` to catch drift between backends.
- `setupCommonEvents` — registers forwarders that copy each Linux event's context into the corresponding Common event and pushes onto `applicationEvents`.
- `monitorPowerEventsCEF` — subscribes to `org.freedesktop.login1.Manager.PrepareForSleep` on the **system** bus (the WebKit backend's `application_linux_dbus.go` is excluded from CEF; we re-implement it here so the CEF build doesn't lose sleep/wake). Probes `NameHasOwner` first so systemd-less distros don't hang.
- `listenForSystemThemeChangesCEF` (now a method on `*linuxApp`) — subscribes to `org.freedesktop.portal.Settings.SettingChanged` on the session bus and emits `events.Linux.SystemThemeChanged`. Same `org.freedesktop.appearance::color-scheme` filter the WebKit backend uses.

Wiring:
- `linuxApp.run()` calls `setupCommonEvents`, `listenForSystemThemeChangesCEF`, and `monitorPowerEventsCEF` BEFORE `cefInit` (so listeners are registered before CEF can fire any callbacks). After `cefInit` succeeds and `markActivated`, a goroutine pushes `events.Linux.ApplicationStartup` onto `applicationEvents`. Going through the central pump means listeners can't block `appRun`.
- `cef_load_handler.go` pushes `events.Linux.WindowLoadStarted` / `WindowLoadFinished` onto `windowEvents` from `OnLoadStart` / `OnLoadEnd` / `OnLoadingStateChange`. The existing `OnLoadEnd` → `HandleMessage("wails:runtime:ready")` is preserved.
- `cefCreateHostWindow` (in `linux_cgo_cef.go`) installs a `GtkEventControllerFocus` via a new C helper `cef_install_focus_controller` (kept in pure C to avoid cgo `uintptr_t → gpointer` strictness). The C callbacks forward to `processWindowEvent` (also redefined here with `//export` so the C linker finds it; the WebKit backend's version in `linux_cgo.go` is excluded from CEF).

`processWindowEvent` is reimplemented here with the same `//export processWindowEvent` + `C.uint` signature the WebKit backend uses, so the C linker finds the symbol from the same `extern` declaration in either build.

**Rationale**:
- The `Common.*` event surface is a cross-platform contract; CEF breaking it would silently break every app subscribing to `events.Common.ApplicationStarted`, etc. A coverage test pins the map.
- Re-implementing `monitorPowerEventsCEF` and `listenForSystemThemeChangesCEF` in CEF-specific files (rather than refactoring the WebKit files into a shared helper) keeps the build tags clean — WebKit keeps `//go:build !cef`, CEF gets its own copy with no risk of breaking WebKit.
- The focus controller has to live in C because GTK4's `g_signal_connect_data` requires a `gpointer` user-data parameter, and cgo's stricter `uintptr_t → gpointer` conversion rules bite us when wiring it from Go.
- `processWindowEvent` lives in the lifecycle file rather than `linux_cgo_cef.go` because the WebKit backend's `linux_cgo.go` is excluded; CEF needs its own (with `//export`) and putting it next to `setupCommonEvents` keeps CEF-specific glue in one file.

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

### Phase 3: IPC & Runtime (✅ COMPLETE)

- [x] V8 handler `wails_invoke` — synchronous Go ← JS calls via `HandleRuntimeCallWithIDs()`
- [x] HTTP POST body + header forwarding from CEF → assetserver (enables the default fetch transport)
- [x] Verified: bound methods work via HTTP POST (`Greeter.Hello("CEF")` → `"Hello CEF from Go!"`)
- [x] Verified: System.Environment, Window.SetTitle via HTTP fetch work end-to-end
- [x] V8 extension context isolation — The native bindings (`wails_invoke` and 7 others) live inside the IIFE that wraps `cef_js_shim.js`, so V8's lexical-scope rule hides them from page scripts (no `window.wails_invoke` ever exists). Three layers of defense-in-depth on the JS wrappers: input validation (rejects null/non-JSON payloads), an optional method allow-list (`Options.Security.AllowedMethods` restricts `Call.ByName(...)` to a known set while keeping built-in subsystems always permitted), and an optional per-navigation CSRF nonce (`Options.Security.EnforceCSRFNonce`, a 16-char hex token from `crypto/rand`) that the wrapper checks against `window._wails.cefNonce`. Default behavior unchanged when both are unset. See Decision C14.
- [x] Async callback resolution — `wails_invokeAsync(callId, payload)` native function dispatches the runtime call to a goroutine (so V8 doesn't block on long-running service methods) and resolves the JS Promise via `frame.ExecuteJavaScript("window._wailsAndroidCallback(callId, response, error)")`. The `wails_callback` round-trip is also wired with a bounded ring buffer for future push-notification subscribers. See Decision C12.
- [x] Event emission — Go → JS event dispatching via `DispatchWailsEvent` → `ExecJS` → `frame.ExecuteJavaScript`, triggered by `OnLoadEnd` signaling runtime-ready
- [x] Window management from JS — `cefMaximiseWindow`, `cefMinimiseWindow`, `cefFullscreenWindow`, `cefMoveWindow` (X11), `cefSetResizable`, `cefSetDecorated`, `cefSetAlwaysOnTop` (X11), `cefGetWindowPosition`, `cefGetCurrentMonitorGeometry`, `cefIsMaximised`, `cefIsMinimised`, `cefIsFullscreen`, `cefIsFocused`, `cefIsVisible`. Verified end-to-end: Maximise/UnMaximise, SetSize, SetPosition, Center, ToggleFrameless, SetResizable all working via puppeteer CDP tests.
- [x] Application lifecycle hooks — `events_common_linux_cef.go` re-implements the WebKit backend's lifecycle glue (`setupCommonEvents` + `monitorPowerEventsCEF` + `listenForSystemThemeChangesCEF`) so CEF exposes the same `events.Linux.ApplicationStartup` / `SystemWillSleep` / `SystemDidWake` / `SystemThemeChanged` events, plus per-window `WindowLoadStarted` / `WindowLoadFinished` from `cef_load_handler` and `WindowFocusIn` / `WindowFocusOut` from a GTK4 `GtkEventControllerFocus` installed by `cef_install_focus_controller`. All forward to their `Common.*` counterparts for cross-platform portability. See Decision C13.
- [x] Drag & drop file events — `cef_drag_handler.go` captures `DragData` in `OnDragEnter` and a new `wails_cefResolveDrop(id, x, y)` native function (routed via `cef_v8_handler.go::handleCefResolveDrop`) returns the captured paths as JSON. The JS shim installs a capture-phase drop listener that forwards the paths to `_wails.handlePlatformFileDrop`, which fires `WindowFilesDropped` through the standard Wails runtime path. See Decision C11.
- [x] System dialogs — `cefDialogHandler` (renderer-initiated `<input type="file">` deferred to Chromium native picker) and `cefJsdialogHandler` (alert/confirm/prompt deferred to Chromium, beforeunload allowed) wired into `cefClientStub`. Go-initiated file dialogs (`window.wails.Dialogs.OpenFile`/`SaveFile`) **disabled** because CEF's `CefBrowserHost::RunFileDialog` triggers SIGSEGV in single-process mode (Chromium V8/Mojo teardown path broken without subprocess isolation, see Decision C1).

### Phase 4: Features & Polish (✅ COMPLETE)

- [x] Multi-window verified (2+ CEF browsers in single process)
- [x] Right-click context menu handler — `cef_context_menu_handler.go` provides Back/Forward/Reload, clipboard, View Source, Print, Inspect Element
- [x] DevTools hotkey (F12) — `cef_keyboard_handler.go` intercepts VK_F12 raw keydown
- [x] CEF auto-download runtime installer — `scripts/download-cef.sh` downloads CEF 147 runtime to `~/.local/share/cef/`
- [x] Multi-process mode investigated — unavailable in the current Go-only process topology. A C++ subprocess helper was tested and rejected after Mojo validation failures; the viable remediation is a native C++ CEF host plus separate Go backend process (Decision C18).
- [x] Window icon via CEF — **GTK4 limitation**: GTK4 removed `gtk_window_set_icon()`. Window icons in GTK4 are set via `.desktop` files. The `linuxApp.setIcon()` method is a no-op (matching the GTK4 WebKit backend behavior).
- [x] Print handler — `cef_print_handler.go` wires `PrintHandler` into the client; `host.Print()` delegates to the native system print dialog

### Phase 5: Wayland (❌ CANCELLED — X11-only per Decision C16)

- [x] Investigate Chrome runtime Ozone/Wayland support — CEF 147 has the Wayland Ozone platform built in. Detached mode works (CEF creates its own Wayland surface) but produces a separate window.
- [ ] CEF surface export via xdg-foreign — not implemented. CEF 147 does not expose the required protocol.
- [x] Wayland detection code — preserved (`cef_wayland_linux.go`, for future use).
- [x] X11-only revert — all Wayland code paths removed. CEF always uses `--ozone-platform=x11 --runtime-style=alloy` with `XReparentWindow` embedding via XWayland. See Decision C16.

## Known Issues

### Critical
- **Multi-process not implemented**: the Go-only host must keep `--single-process`. The helper experiment in Decision C17 still produces Mojo validation failures; the viable remediation is the larger native C++ host + Go backend architecture in Decision C18.

### Medium
- **Go-initiated file dialogs crash**: `CefBrowserHost::RunFileDialog` triggers SIGSEGV in single-process mode when the dialog is dismissed. Chromium V8/Mojo teardown path is broken without subprocess isolation. Renderer-initiated `<input type="file">` dialogs still work via `cefDialogHandler.OnFileDialog` returning 0 (Chromium handles natively).
- **V8 startup snapshot requires manual copy**: CEF 147's build tree places `v8_context_snapshot.bin` in `Release/` but the `resources_dir_path` setting points to `Resources/`. The file must be copied manually. See Decision C16 for the file layout.
- **ICU data must be next to libcef.so**: `icudtl.dat` triggers a FATAL if not present in the same directory as `libcef.so` (CEF issue #3778). We copy it there during setup.
- **V8 Proxy resolver warning**: "Cannot use V8 Proxy resolver in single process mode." Not harmful but logged every startup.
- **nvidia-drm warning**: `Should skip nVidia device named: nvidia-drm`. Not harmful.
- **No user type filter warning**: For CEF built-in extension `cimiefiiaegbelhefglklhhakcgmhkai`. Not harmful.

### Low
- **Deprecated X11 GDK functions**: `gdk_x11_display_get_xdisplay`, `gdk_x11_surface_get_xid` trigger compiler warnings. GTK4 marks them deprecated but no replacement exists for XReparentWindow workflow on GTK4/X11. These are always called now (X11-only, see Decision C16).

## Key Files

### Single-Process CEF Backend (current)
| File | Role |
|------|------|
| `v3/pkg/application/linux_cgo_cef.go` | CGo C code: X11 embedding, reparenting, idle pump, view tracking list |
| `v3/pkg/application/cef_app_stub.go` | CefApp callbacks (OnRegisterCustomSchemes, OnBeforeCommandLineProcessing) |
| `v3/pkg/application/cef_request_handler.go` | Resource request handler (wails:// scheme serving) |
| `v3/pkg/application/cef_load_handler.go` | Load handler (OnLoadEnd → runtime-ready signal) |
| `v3/pkg/application/cef_keyboard_handler.go` | Keyboard handler (F12 → openDevTools) |
| `v3/pkg/application/cef_context_menu_handler.go` | Context menu handler (right-click menu) |
| `v3/pkg/application/cef_print_handler.go` | Print handler (native print dialog) |
| `v3/pkg/application/cef_dialog_handler.go` | DialogHandler (renderer-initiated `<input type="file">`) |
| `v3/pkg/application/cef_drag_handler.go` | DragHandler — captures `DragData` on `OnDragEnter`, exposes paths via `wails_cefResolveDrop` (see Decision C11) |
| `v3/pkg/application/cef_jsdialog_handler.go` | JsdialogHandler (alert/confirm/prompt/beforeunload) |
| `v3/pkg/application/events_common_linux_cef.go` | CEF build's lifecycle glue: Linux→Common event mapping, sleep/wake (logind dbus), theme change (xdg portal), `processWindowEvent` C-callable forwarder (see Decision C13) |
| `v3/pkg/application/cef_wayland_linux.go` | Wayland session detection (see Decision C16). |
| `v3/pkg/application/cef_js_shim.js` | V8 extension JS — declares `wails_*` native functions |
| `v3/pkg/application/dialogs_linux_cef_runtime.go` | CEF-build dialog stubs (Go-initiated file dialogs disabled) |
| `v3/scripts/download-cef.sh` | CEF runtime auto-download script |
| `v3/pkg/application/webview_window_linux_cef.go` | Go-level window creation, init sequence |
| `v3/examples/cef-hello/main.go` | Minimal demo |
| `v3/examples/cef-multiwin/main.go` | Multi-window demo |
| `v3/examples/cef-shadcn-admin/main.go` | Full demo with SPA routing |

### Multi-Process CEF Host (Decision C18 - IN PROGRESS)
| File | Role |
|------|------|
| `v3/cmd/wails-cef-host/CMakeLists.txt` | C++20 build: CEF + GTK4 + X11 |
| `v3/cmd/wails-cef-host/include/host_app.h` | cefApp + cefBrowserProcessHandler declarations |
| `v3/cmd/wails-cef-host/include/window_host.h` | GTK/X11 window embedding wrapper |
| `v3/cmd/wails-cef-host/include/ipc_handler.h` | Unix socket RPC framing + envelope types |
| `v3/cmd/wails-cef-host/src/main.cc` | CefExecuteProcess + gtk_main + message pump |
| `v3/cmd/wails-cef-host/src/host_app.cc` | CEF app init, command-line switches, scheme registration |
| `v3/cmd/wails-cef-host/src/window_host.cc` | GtkSocket → CEF view embedding + resize |
| `v3/cmd/wails-cef-host/src/ipc_handler.cc` | Envelope serialization, Unix socket server |
| `v3/cmd/wails-go-runtime/go.mod` | Go sidecar module |
| `v3/cmd/wails-go-runtime/main.go` | Socket client, MessageProcessor bridge, envelope framing |

## Runtime Dependencies

- `libcef.so` at `CEF_DIR` or `~/.local/share/cef/`
- `icudtl.dat` next to `libcef.so` (CEF issue #3778)
- `Resources/v8_context_snapshot.bin` (must be present in resources dir)
- `Resources/chrome_100_percent.pak`, `chrome_200_percent.pak`, `resources.pak`
- `Resources/locales/` (220 locale files)
- `Release/libEGL.so`, `Release/libGLESv2.so` (loaded via `LD_LIBRARY_PATH` from `CEF_DIR`)
- X11 display (`DISPLAY`, `GDK_BACKEND=x11`) — on Wayland, XWayland is required
- GTK4, libX11

## Verification Matrix

End-to-end tests run via puppeteer-core against `http://127.0.0.1:9999/json` (CDP).

| Capability | Verified | Method | Result |
|---|---|---|---|
| App boots | ✅ | process starts, DevTools port 9999 listening | 1 target detected |
| Asset serving (`wails://`) | ✅ | `cef-hello` and `cef-shadcn-admin` render HTML | tailwind CSS classes apply |
| Bound methods (`wails.Call`) | ✅ | `Greeter.Hello("CEF")` via HTTP POST | `"Hello CEF from Go!"` |
| Bound methods (`Greeter.Add`) | ✅ | `Greeter.Add(3,7)` | `10` |
| Async service methods | ✅ | `wails.Call.ByName("main.Greeter.SlowGreet", "CEF")` via `wails_invokeAsync` | Returns `"Hi CEF (async)"` ~2s later; V8 thread not blocked during the wait |
| Go → JS events | ✅ | `app.Event.Emit("tick", ...)` × 11 over 25s | All received in JS |
| Multi-window | ✅ | `cef-multiwin` opens 2 CEF browsers | Both have OnLoadEnd, both receive events |
| DevTools port | ✅ | CDP `Page.captureScreenshot`, `Runtime.evaluate` | Working |
| F12 DevTools | ✅ | `cef_keyboard_handler.go` intercepts VK_F12 | Window opens (built but not auto-tested) |
| Maximise / UnMaximise | ✅ | `win.Maximise()` → state=max=true; `UnMaximise` → max=false | ✅ |
| Resize | ✅ | `win.SetSize(1024, 768)` → `win.Size()` → `{1024, 768}` | ✅ |
| Move | ✅ | `win.SetPosition(100, 100)` → `win.Position()` → `{100, 141}` (141 = titlebar) | ✅ |
| Center | ✅ | `win.Center()` → `win.Position()` recalculated | ✅ |
| ToggleFrameless | ✅ | `win.ToggleFrameless()` | ✅ |
| SetResizable | ✅ | `win.SetResizable(false)`, `true` | ✅ |
| SetAlwaysOnTop | ✅ | `win.SetAlwaysOnTop(true)` | ✅ |
| IsMaximised / IsMinimised / IsFullscreen / IsFocused | ✅ | state queries reflect GTK state | ✅ |
| Renderer-initiated `<input type="file">` | ✅ | cefDialogHandler returns 0 → Chromium native chooser | Working |
| Application lifecycle events | ✅ | `app.Event.OnApplicationEvent(events.Common.ApplicationStarted, ...)` | Fires after `cefInit` succeeds; `events_common_linux_cef_test.go` pins the Linux→Common mapping |
| Wayland session detection | ✅ | Run `cef-hello` on KDE/Wayland (default session) | Logs `post-init default display backend=wayland-0`; runtime flips to Chrome; CEF creates its own Wayland window |
| Wayland: window positioning NO-OP | ✅ | Call `win.SetPosition(100, 100)` on Wayland | Debug log: `ignoring move to (100, 100): Wayland compositor owns placement`; position unchanged |
| Wayland: embedded CEF view | ❌ | n/a | Phase 5 forward — needs `gtk_shell1` xdg-foreign import; CEF runs detached for now |
| Go-initiated `Dialogs.OpenFile` / `SaveFile` | ❌ → graceful error | `RunFileDialog` SIGSEGV in single-process | Returns "not available in single-process mode" |
| Message dialogs (Info/Warning/Error/Question) | ⚠ no-op | `linuxDialog.show` is empty | Modern apps use JS-side modals |

## Commits (feat/linux-cef branch)

| Date | Hash | Description |
|------|------|-------------|
| 2026-07-07 | 947877e5a | feat(v3/cef): add cef-shadcn-admin demo, fix CORS opaque origin |
| 2026-07-08 | ede680b2c | fix(v3/cef): set CEF Bounds to actual window size, not hardcoded 800x600 |
| 2026-07-10 | 894ec0174 | fix(v3/cef): connect resize signal to GtkWindow, not GtkBox |
| 2026-07-10 | 1cd26cde2 | fix(v3/cef): defer resize via idle callback to catch post-layout size |
| 2026-07-10 | 06f969936 | fix(v3/cef): track CEF views via linked list and resize on idle pump |
| 2026-07-10 | 30ccd0981 | feat(v3/cef): forward POST body and headers from CEF to assetserver |
| 2026-07-10 | c6c5f3a1b | docs: update cef tracker - IPC verified working via HTTP fetch |
| 2026-07-11 | (uncommitted) | feat: implement OnLoadEnd handler for runtime-ready signal, verify Go→JS event dispatch |
| 2026-07-11 | (uncommitted) | feat(v3/cef): F12 DevTools hotkey via cef_keyboard_handler.go |
| 2026-07-11 | (uncommitted) | feat(v3/cef): right-click context menu via cef_context_menu_handler.go |
| 2026-07-11 | (uncommitted) | feat(v3/cef): print handler via cef_print_handler.go |
| 2026-07-11 | (uncommitted) | feat(v3/cef): system dialogs (DialogHandler + JsdialogHandler) |
| 2026-07-11 | (uncommitted) | feat(scripts): download-cef.sh auto-download script |
| 2026-07-11 | (uncommitted) | feat(v3/cef): window management from JS (X11 helpers + GTK4 wrappers) |

---

## Phase 6: Performance for raw-photo workloads (📋 PLANNED — revised 2026-07-12)

**Decision.** This phase is viable only as a **hybrid renderer**. CEF renders the
application UI; a native `GtkGLArea` renders the active RAW photo. CEF must not
be the per-frame photo canvas. The current supported host is X11/XWayland
(Decision C16), where CEF is embedded with `XReparentWindow`. Native Wayland
embedding is not a prerequisite for this phase and is not assumed.

**Why.** A RAW editor needs immediate feedback while sliders, zoom, pan and
brushes change. The first visible result must not wait for LibRaw, disk I/O, or
a Wails binding. The UI keeps slider state locally and applies an approximate
GLSL result immediately; Go concurrently renders a precise preview and returns
it only when its `photoFingerprint`, `recipeRevision`, decoder/profile version,
and size still match the active request. Obsolete renders are cancelled or
dropped.

**Performance model.** The figures below are hypotheses, not verified claims.
They must be measured on the supported CEF build, real hardware, and real RAW
files before being used as acceptance criteria.

| Path | Expected role | Performance status |
|---|---|---|
| Native CEF child window over X11/XWayland | Wails UI | Existing supported path; not the RAW canvas |
| Native `GtkGLArea` + GLSL | Active photo canvas | Target path for 60 FPS interaction; validate on target GPUs |
| CEF OSR → CPU BGRA → GTK/Cairo | Wayland-native UI research only | Unsuitable for the per-frame photo canvas because every frame incurs readback/copy |
| Detached CEF Wayland window | Unsupported UX | Not a viable editor host: it leaves the GTK host separate |

**Non-goals.** Multi-process CEF (Decision C18) improves UI isolation and can
enable GPU-backed Chromium features, but it does not make RAW demosaic,
preview generation, or CEF-to-GL composition free. A C++ subprocess alone also
does not provide zero-copy Wayland surface sharing; that would require a CEF
surface-export API and a separate, validated compositor design.

### Phase 6A — NativeCanvas vertical slice (first implementation)

**Target:** Prove the hybrid architecture on X11/XWayland with one real preview
texture and measured input-to-paint latency. The first version uses a split or
explicitly assigned canvas region. It does **not** promise a transparent CEF
overlay: a reparented native CEF child window cannot safely be stacked and
composited like a regular GTK widget.

**API proposal (Go):**

```go
type NativeCanvas struct { /* ... */ }

// AttachNativeCanvas creates a GtkGLArea in an explicitly assigned content
// region. It returns the existing canvas if already attached. CEF and GL
// start as sibling regions; overlay composition is deferred to a future OSR
// or compositor design.
func (w *linuxWebviewWindow) AttachNativeCanvas() *NativeCanvas

// Detach removes the GL area. The window reverts to CEF-only mode.
func (nc *NativeCanvas) Detach()

// SetRenderCallback registers a per-frame render callback invoked on
// the main thread. The state exposes the GL context, framebuffer size,
// device-pixel-ratio. App code calls its GLSL pipeline here.
func (nc *NativeCanvas) SetRenderCallback(cb func(state *NativeRenderState))

// SetResizeCallback fires when the canvas is resized.
func (nc *NativeCanvas) SetResizeCallback(cb func(w, h int))
```

**Tasks:**

| Work item | Outcome |
|---|---|
| Canvas lifecycle | Idempotent attach/detach, resize and context-loss cleanup tests |
| GL presentation | A demo uploads and presents a compact preview texture; no RAW matrix crosses Wails IPC |
| Input ownership | Pointer/keyboard events go to the canvas only inside its region and to CEF only inside the UI region |
| Preview protocol | Requests carry photo fingerprint, recipe revision, output size and priority; stale results cannot replace the active image |
| Cancellation and queues | Separate active-preview, thumbnail and export queues; active preview wins and old slider work is cancelled |

**Acceptance measurements:** capture baseline and post-change p50/p95 for
input-to-paint, approximate-slider paint, precise-preview completion, cache
hit rate, memory, and dropped stale work. Run continuous slider drag, rapid
photo switching, and library sizes of 1,000/10,000/50,000 images. Do not claim
60 FPS, 200 FPS, or a cache benefit without these measurements.

### Phase 6B — OSR research (only if native Wayland becomes a product goal)

**Target:** Determine whether CEF UI panels can be embedded natively in a GTK
Wayland window without regressing input or memory. This is separate from the
photo canvas and must not block Phase 6A.

The investigation includes complete GTK input forwarding, dirty-rectangle
handling, frame coalescing, buffer reuse, resize coalescing, IME behaviour,
and visual regression testing. Its exit criterion is a measured UI result on
real hardware, not a preselected FPS estimate. If it does not meet the UI
budget, XWayland remains the supported deployment path.

**Out of scope:** zero-copy CEF-to-GL composition on Wayland, transparent
native-window overlay, multi-touch, and drag-and-drop between CEF and the
canvas. Each needs its own design and benchmark before scheduling.
