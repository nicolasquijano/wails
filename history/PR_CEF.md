# Pull Request: feat/linux-cef

**Branch**: `feat/linux-cef` → `master`
**Base**: `master` at `8a48f73ba` (pre-PR)
**HEAD**: see current `git log feat/linux-cef ^master --oneline`

## Summary

Adds an opt-in **CEF (Chromium Embedded Framework) backend** for Wails v3
on Linux. With `-tags cef`, applications render through a real Chromium
runtime instead of WebKitGTK, which unlocks Widevine L1, proprietary
codecs (H.264/AAC), WebUSB/Bluetooth/Serial, and a consistent Chromium
feature set on Linux.

The branch delivers the build-tag plumbing, the asset-server bridge, the
JS↔Go IPC, the doctor-ng integration, the example app, and the
documentation. CEF 147 init now reaches "browser created, assetserver
serving `wails://localhost/`" end-to-end on a Wayland session with
`GDK_BACKEND=x11`; visual rendering is pending a fix for an X11
`MatchError` that's documented as a follow-up issue.

## Commits

```
fix(v3/linux): unblock CEF 147 end-to-end pipeline
docs: add CEF continuation handoff
fix(v3/linux): dispatch CEF windows on GTK main loop
docs(IMPLEMENTATION): phase 4.6 — browser creation deadlock
build(v3/linux): phase 4.5 — subprocess detection + file-based debug log
build(v3/linux): phase 4.4 — register wails scheme, debug logging
build(v3/linux): phase 4.3 — pass parent XID to BrowserHostCreateBrowserSync
build(v3/linux): phase 4.1 — fix init order + appID bugs found via smoke test
build(v3/linux): phase 4 — body streaming + return values + flags/env + events
build(v3/linux): phase 3 — JS↔Go IPC via CefV8Handler + RegisterExtension
build(v3/linux): phase 2 — asset server routing for wails:// URLs
build(v3/linux): phase 1 — first end-to-end CEF build (purego-cef + GTK4 host)
build(v3/linux): phase 0 — add !cef build tag scaffolding for Linux backend
docs: correct CEF binding version (energye/v3 v3.0.16) + add ecosystem analysis
docs: add CEF/Chromium Embedded implementation plan (Linux opt-in)
```

## What's in this PR

### Build tag
- New `-tags cef` build mode for Linux. Mutually exclusive with the
  default `webgtk` (GTK4 + WebKitGTK 6.0) and `gtk3` (legacy) modes.
- `pkg/application/application_linux_cef.go` and `pkg/application/linux_cgo_cef.go`
  are the new `linux && cgo && cef` build-tagged files.
- All four modes compile: `''`, `gtk3`, `server`, `cef`.
- `pkg/application` tests pass in default, `gtk3`, and `cef` modes.

### Runtime
- `init()` forces `GDK_BACKEND=x11` and drops `WAYLAND_DISPLAY` so GTK
  picks X11 even on a Wayland compositor (KDE Plasma verified). The
  CEF view is reparented into a GTK window via XReparentWindow, which
  only works on real X11 surfaces.
- Helper subprocess detection routes through `cef.MaybeExitSubprocess()`
  instead of `os.Exit(0)`. The earlier `os.Exit` short-circuited CEF's
  own `cef_execute_process` and left GPU/zygote/renderer processes
  unable to register themselves — the GPU process would silently
  "crash" (because it never started) and Chromium would abort with
  `FATAL:content/browser/gpu/gpu_data_manager_impl_private.cc:417`.
- `cefInit` now calls `gtk_init()` before `cef.Init` so the X11 display
  is open and CEF reuses it. CEF's internal GTK init was otherwise
  competing with ours and winning, which made GTK fall back to
  Wayland.
- CEF message pump is wired to the GTK idle queue via
  `g_idle_add_full` at `G_PRIORITY_DEFAULT_IDLE` so the GTK main loop
  drives `cef.DoMessageLoopWork()` while `ExternalMessagePump: true`.
- A `cef.App` (`pkg/application/cef_app_stub.go`) injects the
  Chromium command-line switches that make CEF usable on forced-X11:
  - `--ozone-platform=x11` — Ozone defaults to Wayland otherwise
  - `--disable-gpu` + `--in-process-gpu` — the GPU sandbox can't start
    on XWayland; software rasterization is fine for our use case
  - `--runtime-style=alloy` — CEF 147's default Chrome runtime
    ignores `WindowInfo.ParentWindow`; Alloy does

### Asset-server bridge
- CEF's `RegisterSchemeHandlerFactory` is called once at startup to
  bind the `wails` and `wails.localhost` schemes to the wails
  `assetserver.Handler`. A CEF request for `wails://localhost/` lands
  in the same router the HTTP and WebSocket transports use.
- `body streaming + return values + flags/env` (Phase 4): CEF's
  resource handler reads request bodies, streams responses, and
  reflects `Window.Options.Flags` / `.Environment` into the V8
  context.

### JS↔Go IPC (Phase 3)
- `RegisterExtension` installs a CEF V8 extension that exposes
  `window.wails.invoke(...)` to the renderer. A `CefV8Handler` routes
  calls through the wails `MessageProcessor`, the same router used by
  HTTP and WebSocket.
- `wails_setFlags` / `wails_setEnvironment` are wired through
  `OnDocumentAvailableInMainFrame`.

### doctor-ng (Phase 5)
- New `cef (opt-in)` dependency category on Linux in
  `pkg/doctor-ng/platform_linux.go`. Missing CEF is not an error.
- Per-distro install hints in
  `pkg/doctor-ng/packagemanager/{pacman,apt,dnf,emerge,eopkg,nixpkgs,xbps,zypper}.go`.
  AUR is documented as a known broken case (jellyfin-desktop-libcef-bin
  is CEF 146; we need ≥147).

### Example
- `v3/examples/cef-hello/` — minimum-viable example. Inline HTML, no
  frontend bundle needed. ~80 LOC.

### Documentation
- `v3/docs/guides/cef.md` — when to use, per-distro install, runtime
  configuration, troubleshooting (CEF < 147, GPU process on Wayland,
  GtkWindow/visual `MatchError`), architecture diagram.
- `history/CEF_HANDOFF.md` — continuation document; this PR supersedes
  the CEF 126-based smoke tests in earlier session notes.
- `IMPLEMENTATION.md` — phases 0-6 with commits, file lists, and
  decision log.

## What this PR does NOT do (follow-ups)

1. **Visual rendering on forced-X11 sessions is broken** by an X11
   `MatchError`. CEF (under `--ozone-platform=x11`) uses the X display's
   default visual (24-bit TrueColor); GTK4 on KDE Wayland picks an
   ARGB32 visual from `_NET_VISIBLE`. The two don't share a visual,
   so `XCreateWindow` for the CEF view fails with `Match`. The work
   around used in this branch is to let CEF create a top-level window
   (no `ParentWindow`) and reparent it into the GtkBox via
   `XReparentWindow`. The browser renders, the assetserver serves, but
   the screenshot tooling returns black on this XWayland host so we
   can't visually confirm.
   - **Follow-up issue**: `XCreateWindow MatchError on GTK4 + CEF 147
     + XWayland` — investigate forcing GTK to use the X display
     default visual, or use CEF's `CefWindowInfo.SetAsWindowless` for
     a software-only path.
2. **The browser view doesn't auto-resize with the GtkBox** because
   we never wire ConfigureNotify on the host window. Phase 4 resize
   tracking is still pending; currently the CEF view sticks to its
   800×600 initial bounds until you explicitly call `setSize`.
3. **No smoke test on a real X11 session**. The XWayland capture
   path returns black; we have logs showing the pipeline is correct
   end-to-end but no visual confirmation that the HTML actually paints.
4. **CI workflow for `-tags cef`** is deferred until the visual
   rendering path is solid.
5. **Push to `origin/feat/linux-cef` is blocked** by missing
   credentials in this environment. Reviewers can pull the local
   branch and run `go test -tags cef ./pkg/application` to verify the
   pipeline portion.

## Verification matrix

| Build mode | `go build` | `go test ./pkg/application` |
|------------|------------|-----------------------------|
| default    | ✅          | ✅                           |
| `-tags gtk3`  | ✅       | ✅                           |
| `-tags server` | ✅      | n/a                          |
| `-tags cef`   | ✅       | ✅                           |

CEF 147 init smoke test (host: CachyOS, KDE Wayland, XWayland):

```
$ DISPLAY=:1 GDK_BACKEND=x11 CEF_DIR=~/cef147std/.../Release /tmp/cef-hello
[cefInit] post-init default display backend=:1
[cefCreateBrowserInWidget] url="wails://localhost/" gtkWindowXID=...
[cefCreateBrowserInWidget] returned browser=true
[cefCreateBrowserInWidget] browser view XID=...
[OnBeforeResourceLoad] url="wails://localhost/" isAsset=true
```

No `MatchError` after the reparent-to-GtkBox fix; CEF log is warnings
only. The window is in the WM's `_NET_CLIENT_LIST` (verified via
`xprop -root`). Visual paint is the only unverified step.

## Files added

```
v3/pkg/application/application_linux_cef.go
v3/pkg/application/linux_cgo_cef.go
v3/pkg/application/webview_window_linux_cef.go
v3/pkg/application/cef_client_stub.go
v3/pkg/application/cef_request_bridge.go
v3/pkg/application/cef_request_handler.go
v3/pkg/application/cef_v8_handler.go
v3/pkg/application/cef_js_shim.go
v3/pkg/application/cef_js_shim_embed.go
v3/pkg/application/cef_app_stub.go
v3/pkg/application/clipboard_linux_cef.go
v3/pkg/application/dialogs_linux_cef.go
v3/pkg/application/menu_global_shortcut_linux_cef.go
v3/examples/cef-hello/main.go
v3/examples/cef-hello/README.md
v3/docs/guides/cef.md
```

## Files modified

```
v3/pkg/application/application.go
v3/pkg/doctor-ng/platform_linux.go
v3/pkg/doctor-ng/packagemanager/{pacman,apt,dnf,emerge,eopkg,nixpkgs,xbps,zypper}.go
v3/Taskfile.yaml
IMPLEMENTATION.md
history/CEF_HANDOFF.md
```

## Reviewer checklist

- [ ] `cd v3 && go build ./...` (default mode)
- [ ] `cd v3 && go test ./pkg/application` (default mode)
- [ ] `cd v3 && go test -tags cef ./pkg/application` (CEF mode)
- [ ] `cd v3/examples/cef-hello && go build -tags cef -o /tmp/cef-hello .`
- [ ] (Optional) `DISPLAY=:1 GDK_BACKEND=x11 CEF_DIR=<path-to-cef-147> /tmp/cef-hello`
      on a machine with CEF 147 and check `/tmp/wails-cef-debug.log`
      and `/tmp/wails-cef.log` for the happy path shown above.
