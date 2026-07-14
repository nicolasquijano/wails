# Wails v3 with CEF (Chromium Embedded Framework) on Linux

CEF is an **opt-in, third backend** for Wails v3 on Linux. It sits
alongside the existing webgtk (default) and gtk3 (legacy) backends
and is enabled with the `cef` build tag.

```
go build -tags cef -o myapp .
```

This guide covers installation, build flags, runtime configuration,
and troubleshooting.

## When to use CEF

Choose CEF if you need any of the following and the default webgtk
(WebKitGTK 6.0) doesn't deliver them:

- **Widevine / L1 DRM** for paid streaming (Netflix, Disney+, etc.)
- **WebUSB**, **WebBluetooth**, **WebSerial** — WebKit support is patchy
- **Proprietary media codecs** (H.264/AAC in <video> tags) — WebKit
  on Linux ships without them by default
- **Chromium-only APIs** — modern CSS, Paint Holding, etc.
- **Identical rendering** across Linux/macOS/Windows (CEF runs the
  same Chromium version everywhere)

If you only need a basic webview, stick with the default webgtk — it's
smaller, faster to build, and uses the system-installed WebKit (no
extra ~150MB of CEF binaries).

## Prerequisites

### 1. CEF runtime (libcef.so)

You need **CEF 147 or newer** installed on the host. The exact
version is enforced by the binding at startup; older versions
(CEF 126 in Steam, CEF 127 in OBS, CEF 146 in jellyfin-desktop)
will fail with `undefined symbol: cef_set_nestable_tasks_allowed`.

#### Install on CachyOS / Arch Linux (recommended)

The easiest path is `yay` from the AUR. As of 2026-07 the AUR has
jellyfin-desktop-libcef-bin at CEF 146 which is too old; use the
upstream tarball instead:

```sh
# 1. Download the upstream minimal tarball
sudo mkdir -p /opt/cef
cd /tmp
curl -L -o cef.tar.bz2 \
  'https://cef-builds.spotifycdn.com/cef_binary_147.4.5+g5e8a8e7+chromium-147.4.5_linux64_minimal.tar.bz2'

# 2. Extract (strip the leading directory)
sudo tar -xjf cef.tar.bz2 -C /opt/cef --strip-components=1

# 3. Tell wails where to find it (pick one)
sudo ln -s /opt/cef/libcef.so /usr/lib/cef/libcef.so   # system-wide
export CEF_DIR=/opt/cef                                 # or per-process

# 4. Verify
ls /opt/cef/{libcef.so,icudtl.dat,chrome_100_percent.pak}
```

#### Install on Debian / Ubuntu

There is no official CEF package. Extract the upstream tarball:

```sh
sudo mkdir -p /opt/cef
curl -L -o /tmp/cef.tar.bz2 \
  'https://cef-builds.spotifycdn.com/cef_binary_147.4.5+g5e8a8e7+chromium-147.4.5_linux64_minimal.tar.bz2'
sudo tar -xjf /tmp/cef.tar.bz2 -C /opt/cef --strip-components=1
export CEF_DIR=/opt/cef
```

#### Install on Fedora / RHEL

Same tarball approach (CEF isn't in the Fedora repos):

```sh
sudo mkdir -p /opt/cef
curl -L -o /tmp/cef.tar.bz2 \
  'https://cef-builds.spotifycdn.com/cef_binary_147.4.5+g5e8a8e7+chromium-147.4.5_linux64_minimal.tar.bz2'
sudo tar -xjf /tmp/cef.tar.bz2 -C /opt/cef --strip-components=1
export CEF_DIR=/opt/cef
```

#### Install on NixOS

CEF is in nixpkgs:

```nix
environment.systemPackages = [ pkgs.cef ];
```

#### Install on Arch via AUR (CEF 146 — DOES NOT WORK)

```sh
yay -S jellyfin-desktop-libcef-bin
```

This installs CEF 146, which is **not compatible** with the current
binding. Use the upstream tarball method above instead.

### 2. GTK4 (host window)

The CEF backend embeds the browser in a GTK4 window, so you still
need GTK4 installed (just like webgtk). On CachyOS:

```sh
sudo pacman -S gtk4
```

### 3. Go 1.25+ and a C toolchain

The binding uses cgo, so you need `gcc` and `pkg-config`.

## Build

```sh
cd /path/to/your/wails-app
go build -tags cef -o myapp .
```

The `-tags cef` switch enables the CEF backend and excludes the
default webgtk backend. Without it you get the same default build
as before.

You can also pin the CEF version override (advanced):

```sh
go build -tags cef -ldflags '-X main.CEFVersion=147' -o myapp .
```

## Run

```sh
# If libcef.so is in /usr/lib/cef, no env vars needed.
./myapp

# Otherwise, tell the runtime where CEF lives:
CEF_DIR=/opt/cef ./myapp

# Or skip the version check entirely (for debugging):
CEF_DIR=/opt/cef CEF_SKIP_VERSION_CHECK=1 ./myapp
```

Expected behaviour with CEF 147 installed:

1. GTK4 window opens.
2. CEF renders the URL configured in `WebviewWindow.URL`
   (`wails://` and `http://wails.localhost/` are intercepted by
   the Wails assetserver).
3. JS calls to `window.wails.invoke(...)` are routed to the
   Go-side message processor.

## Troubleshooting

### `dlopen: libcef.so: cannot open shared object file`

The library can't be found. Either:

- Install CEF into `/usr/lib/cef/` (system path).
- Set `CEF_DIR=/path/to/cef` to the directory containing `libcef.so`.
- Add the directory to `LD_LIBRARY_PATH`.

### `undefined symbol: cef_set_nestable_tasks_allowed`

You're using CEF < 147. The binding requires CEF 147+. Either
upgrade CEF or downgrade the binding (not currently supported).

### `cef_initialize returned 0`

CEF failed to initialize. Common causes:

- **No display server**: Wails needs X11 or Wayland. On headless
  systems, run via `xvfb-run -a ./myapp`.
- **Sandbox blocked**: try `--no-sandbox` mode (already on by
  default) or check AppArmor / SELinux.
- **Missing icudtl.dat / .pak files**: the tarball must be extracted
  with all its files, not just `libcef.so`.

### `Gtk-CRITICAL ... gtk_application_new: ...`

Your `Options.Name` produces an invalid D-Bus application ID.
The CEF build lowercases and prefixes `io.wails.` automatically,
but if you see this, double-check `options.Name` is non-empty.

### `panic: cef: ref manager not initialized; call cef.Init() first`

Internal bug: a CEF API was called before `cef.Init()` succeeded.
Please open an issue with the full stack trace.

### `cef: ExecuteSubprocess: ...`

Expected during startup; it's a no-op for the main process. CEF
spawns helper subprocesses (renderer, GPU, etc.) which use the
same binary to detect their role via `cef.MaybeExitSubprocess`.

### Window opens but is blank / black

The CEF view was created but the XReparentWindow into the GTK widget
didn't complete. Try setting `CEF_DIR` and confirming that the GTK
window has been realized (visible) before CEF creates the browser.

## Diagnostics

`wails doctor` (doctor-ng) lists the CEF dependency as "cef (opt-in)"
on Linux. It's marked optional; missing CEF is not an error.

```sh
$ wails doctor
✓ gtk4 (gtk4 4.x.x installed)
✓ webkitgtk-6.0 (webkitgtk-6.0 6.x.x installed)
○ cef (opt-in) — requires libcef 147+
  Install CEF 147 to /usr/lib/cef or set CEF_DIR.
```

For verbose CEF logs, set:

```sh
CEF_DIR=/opt/cef ./myapp --enable-logging=stderr --v=1
```

## Architecture summary

```
your-app (Go)
├─ pkg/application/application_linux_cef.go   # linuxApp + cefInit
├─ pkg/application/webview_window_linux_cef.go # linuxWebviewWindow
├─ pkg/application/cef_cgo_cef.go              # cgo: GTK host window + X11 reparent
├─ pkg/application/cef_request_handler.go     # wails:// → assetserver
├─ pkg/application/cef_v8_handler.go          # window.wails.* → MessageProcessor
└─ pkg/application/cef_js_shim.js              # V8 extension injected into every frame

libcef.so (CEF 147)
├─ GTK4 widgets (GtkApplicationWindow + GtkBox, managed from Go)
└─ Chromium renderer (in subprocess) renders into reparented X11 window
```

For the **multi-process backend** (Decision C18, M7 done; M8 pending),
the architecture is split:

```
your-app (Go) — same as above, plus opt-in fork of the sidecar
wails-cef-host (C++ browser process) — GTK3 host widgets (Decision C19)
├─ CefInitialize, CefBrowser, GTK windows, X11 reparent
├─ asset/V8 → Unix-socket RPC
└─ forks → zygote → renderer, GPU, utility, network
wails-go-runtime (Go sidecar) — no GTK/CEF imports
└─ MessageProcessor, services, assets, events, SQLite
```

The CEF bridge lives in `pkg/application/`. The internal
`assetserver` package is unaware of CEF. The `MessageProcessor`
is the same router that HTTP and WebSocket transports use.

## Building from source (advanced)

If you want to bundle CEF inside your binary (so users don't need
to install it separately), use a build tag with `-tags cef_static`:

```sh
# 1. Add the tarball's contents to your source tree:
mkdir -p ./internal/cef-runtime
cp -r /path/to/cef/ ./internal/cef-runtime/

# 2. Set CEF_DIR via build tag (requires patching pkg/application/cef.go)
```

This is **not yet supported** in upstream Wails but the
infrastructure is in place. See `phase 6` of `v3/IMPLEMENTATION.md`
for the roadmap.

## Multi-process bundle (Decision C18, M7)

The single-process backend above is the only path that runs today.
For development, packaging, and shipping, the multi-process backend
is wired up but requires the **full CEF SDK** (headers +
`libcef_dll_wrapper.a`) plus `cmake`, `g++`, `go`, `patchelf`,
`pkg-config`, and (for headless verification) `xvfb` + `curl`.

### Build the bundle

```sh
export CEF_DIR=/opt/cef
task build:cef:bundle EXAMPLE=cef-hello BUNDLE_OUT=./dist/cef-bundle
```

This produces a self-contained directory:

```
dist/cef-bundle/
├── run.sh                          # wrapper (sets LD_LIBRARY_PATH)
├── README.txt                      # troubleshooting notes
├── bin/
│   ├── cef-hello                   # Go app (-tags cef)
│   ├── wails-cef-host              # C++ browser process
│   └── wails-go-runtime            # Go sidecar (MessageProcessor)
├── lib/
│   ├── libcef.so
│   ├── libEGL.so
│   └── libGLESv2.so
├── icudtl.dat
└── Resources/
    ├── v8_context_snapshot.bin
    ├── *.pak
    └── locales/
```

### Run

```sh
# Headless smoke test (CI):
xvfb-run -a ./dist/cef-bundle/run.sh

# Or smoke-test a built bundle directly:
task verify:cef:bundle BUNDLE_DIR=./dist/cef-bundle
```

### Validation gates

The build script enforces Decision C16 file layout at packaging time
(missing `libcef.so`, `icudtl.dat`, `Resources/v8_context_snapshot.bin`
abort with an actionable error). At startup, the C++ host re-runs the
same checks via `wails_cef::ValidateCefDistribution` so a corrupted
bundle is detected before `CefInitialize` is called.

Unit tests for the validation logic (no CEF/GTK dependency):

```sh
task test:cef:validate
# 25/25 checks pass without needing CEF installed
```

### Status

The bundle is **built and validated** but the multi-process backend
itself is **not yet functional** end-to-end — the C++ host has placeholders
for `posix_spawn` of the Go sidecar (M2 in Decision C18). The bundle
runs, but renderer/zygote subprocesses won't appear unless M2/M3 are
landed. See `CEF_IMPLEMENTATION.md` for the M2/M3/M8 follow-up work.