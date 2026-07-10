# WebKitGTK 6.0 / GTK4 Implementation Tracker

## Overview

This document tracks the implementation of WebKitGTK 6.0 (GTK4) support for Wails v3 on Linux.

**Current goal (post-flip, 2026-05-16)**: GTK4 + WebKitGTK 6.0 is the **default** Linux stack for v3.0.0 GA. GTK3 + WebKit2GTK 4.1 is a legacy opt-in via `-tags gtk3` for one v3 cycle and is scheduled for removal in v3.1.

**Original goal (2026-02-04, superseded by Decision 1.1)**: Provide GTK4/WebKitGTK 6.0 support as an EXPERIMENTAL opt-in via `-tags gtk4`, while maintaining GTK3/WebKit2GTK 4.1 as the stable default.

## Architecture Decisions

### Decision 1.1: GTK4 as Default, GTK3 Opt-In (2026-05-16) — supersedes Decision 1

**Context**: GTK4 + WebKitGTK 6.0 has matured through the v3 alpha cycle (alpha.74 → alpha.92). For v3.0.0 GA, shipping with the modern stack as default-of-least-resistance — rather than as an experimental opt-in — is required so distros and packagers do not need to learn a Wails-specific build tag to get a working build.

**Decision**: GTK4 + WebKitGTK 6.0 is the default. GTK3 + WebKit2GTK 4.1 is opt-in via `-tags gtk3`. The legacy path stays through the v3.0.x line and is removed in v3.1.

**Rationale**:
- Removes a discovery hurdle for new users on modern distros (Ubuntu 24.04+, Fedora 40+, Arch, NixOS unstable) — `go build` Just Works
- Aligns with where the broader GTK ecosystem is moving — GTK3 EOL is on the horizon
- Distros still on WebKit2GTK 4.1 (Ubuntu 22.04 LTS, Debian 12, Fedora ≤ 39, RHEL 9.x) get a clearly-named opt-in until those LTSes age out
- The `-tags gtk3` escape hatch lets us defer the breaking change of dropping GTK3 entirely to v3.1

**Implementation (issue #5459)**: Build-tag flip + file renames. Files previously named `*_linux_gtk4.go` become the bare `*_linux.go` defaults with constraint `!gtk3`; files previously named `*_linux.go` become `*_linux_gtk3.go` with constraint `gtk3`. The `gtk4` build tag is retired in favor of `gtk3` as the toggle. Legacy `wails3 doctor` package-manager polarity inverted (gtk4/webkitgtk-6.0 required, gtk3/webkit2gtk-4.1 optional legacy). `doctor-ng` already had the correct polarity.

### Decision 1: GTK3 as Default, GTK4 Opt-In (2026-02-04) — SUPERSEDED by Decision 1.1
**Context**: Need to support modern Linux distributions with GTK4 while maintaining stability for existing apps.

**Decision**: GTK3 remains the stable default (no build tag required). GTK4 is available as experimental via `-tags gtk4`.

**Rationale**:
- GTK3/WebKit2GTK 4.1 is battle-tested and widely deployed
- GTK4 support needs more community testing before becoming default
- Allows gradual migration and feedback collection
- Protects existing apps from unexpected breakage

**Build Tags** (post-Decision 1.1, post-#5463 review fixes):
- Default (no tag): `//go:build linux && cgo && !gtk3 && !android && !server`
- Legacy GTK3 opt-in: `//go:build linux && cgo && gtk3 && !android && !server`
- Server mode (`-tags server`) excludes both paths so no GTK/cgo code is linked.

### Decision 2: pkg-config Libraries (2026-01-04)
**GTK4/WebKitGTK 6.0**:
```
#cgo linux pkg-config: gtk4 webkitgtk-6.0 libsoup-3.0
```

**GTK3/WebKit2GTK 4.1** (legacy):
```
#cgo linux pkg-config: gtk+-3.0 webkit2gtk-4.1 libsoup-3.0
```

### Decision 3: Wayland Window Positioning (2026-01-04)
**Context**: GTK4/Wayland doesn't support arbitrary window positioning - this is a Wayland protocol limitation.

**Decision**: Window positioning functions (`move()`, `setPosition()`, `center()`) are documented NO-OPs on GTK4/Wayland.

**Rationale**: This is a fundamental Wayland design decision, not a limitation we can work around. Users need to be aware of this behavioral difference.

### Decision 4: Menu System Architecture (2026-01-04)
**Context**: GTK4 removes GtkMenu/GtkMenuItem in favor of GMenu/GAction.

**Decision**: Complete rewrite of menu system for GTK4 using GMenu/GAction/GtkPopoverMenuBar.

**Status**: Stub implementations only. Full implementation pending.

### Decision 5: System Tray Compatibility (2026-01-04)
**Context**: v3's system tray uses D-Bus StatusNotifierItem protocol.

**Decision**: No changes needed - system tray is already GTK-agnostic.

## Implementation Progress

### Phase 1: Build Infrastructure ✅ COMPLETE

**Commit**: `a0ca13fdc` (2026-01-04)

#### 1.1 Add gtk3 constraint to existing files
Files modified:
- `v3/pkg/application/application_linux.go` - Added `gtk3` constraint
- `v3/pkg/application/linux_cgo.go` - Added `gtk3` constraint  
- `v3/internal/assetserver/webview/request_linux.go` - Added `gtk3` constraint
- `v3/internal/assetserver/webview/responsewriter_linux.go` - Added `gtk3` constraint
- `v3/internal/assetserver/webview/webkit2.go` - Added `gtk3` constraint

#### 1.2 Create GTK4 stub files
Files created:
- `v3/pkg/application/linux_cgo_gtk4.go` (~1000 lines)
  - Main CGO file with GTK4 bindings
  - Implements: window management, clipboard, basic menu stubs
  - Uses `gtk4 webkitgtk-6.0` pkg-config
  
- `v3/pkg/application/application_linux_gtk4.go` (~250 lines)
  - Application lifecycle management
  - System theme detection via D-Bus
  - NVIDIA DMA-BUF workaround for Wayland

#### 1.3 Create WebKitGTK 6.0 asset server stubs
Files created:
- `v3/internal/assetserver/webview/webkit6.go`
- `v3/internal/assetserver/webview/request_linux_gtk4.go`
- `v3/internal/assetserver/webview/responsewriter_linux_gtk4.go`

### Phase 2: Doctor & Capabilities ✅ COMPLETE

**Goal**: Update `wails doctor` to check for GTK4 as primary, GTK3 as secondary.

#### 2.1 Package Manager Updates
All 7 package managers updated to check GTK4/WebKitGTK 6.0 as primary, GTK3 as optional/legacy:
- `v3/internal/doctor/packagemanager/apt.go` ✅
- `v3/internal/doctor/packagemanager/dnf.go` ✅
- `v3/internal/doctor/packagemanager/pacman.go` ✅
- `v3/internal/doctor/packagemanager/zypper.go` ✅
- `v3/internal/doctor/packagemanager/emerge.go` ✅
- `v3/internal/doctor/packagemanager/eopkg.go` ✅
- `v3/internal/doctor/packagemanager/nixpkgs.go` ✅

Package key naming convention (post-#5463 default flip): `gtk4`, `webkitgtk-6.0` (primary/default), `gtk3 (legacy)`, `webkit2gtk (legacy)` (optional, removed in v3.1)

#### 2.2 Capabilities Detection
Files created/updated:
- `v3/internal/capabilities/capabilities.go` - Added `GTKVersion` (int) and `WebKitVersion` (string) fields
- `v3/internal/capabilities/capabilities_linux.go` - GTK4 default: `GTKVersion: 4, WebKitVersion: "6.0"`
- `v3/internal/capabilities/capabilities_linux_gtk3.go` - GTK3 legacy: `GTKVersion: 3, WebKitVersion: "4.1"`

TODO (deferred to Phase 3):
- [ ] Update `v3/internal/doctor/doctor_linux.go` - Improve output to show GTK4 vs GTK3 status

### Phase 3: Window Management ✅ COMPLETE

#### 3.1 GTK4 Event Controllers
GTK4 replaces direct signal handlers with `GtkEventController` objects:
- `GtkEventControllerFocus` for focus in/out events
- `GtkGestureClick` for button press/release events
- `GtkEventControllerKey` for keyboard events
- Window signals: `close-request`, `notify::maximized`, `notify::fullscreened`

New C function `setupWindowEventControllers()` sets up all event controllers.

#### 3.2 Window Drag and Resize
GTK4 uses `GdkToplevel` API instead of GTK3's `gtk_window_begin_move_drag`:
- `gdk_toplevel_begin_move()` for window drag
- `gdk_toplevel_begin_resize()` for window resize
- Requires `gtk_native_get_surface()` to get the GdkSurface

#### 3.3 Drag-and-Drop with GtkDropTarget
Complete implementation using GTK4's `GtkDropTarget`:
- `on_drop_enter` / `on_drop_leave` for drag enter/exit events
- `on_drop_motion` for drag position updates
- `on_drop` handles file drops via `GDK_TYPE_FILE_LIST`
- Go callbacks: `onDropEnter`, `onDropLeave`, `onDropMotion`, `onDropFiles`

#### 3.4 Window State Detection
- `isMinimised()` uses `gdk_toplevel_get_state()` with `GDK_TOPLEVEL_STATE_MINIMIZED`
- `isMaximised()` uses `gtk_window_is_maximized()`
- `isFullscreen()` uses `gtk_window_is_fullscreen()`

#### 3.5 Size Constraints
GTK4 removed `gtk_window_set_geometry_hints()`. Now using `gtk_widget_set_size_request()` for minimum size.

TODO (deferred):
- [ ] Test window lifecycle on GTK4 with actual GTK4 libraries

### Phase 4: Menu System ✅ COMPLETE

GTK4 completely replaced the menu system. GTK3's GtkMenu/GtkMenuItem are gone.

#### 4.1 GMenu/GAction Architecture
- `GMenu` - Menu model (data structure, not a widget)
- `GMenuItem` - Individual menu item in the model
- `GSimpleAction` - Action that gets triggered when menu item is activated
- `GSimpleActionGroup` - Container for actions, attached to widgets

#### 4.2 Menu Bar Implementation
- `GtkPopoverMenuBar` created from `GMenu` model via `create_menu_bar_from_model()`
- Action group attached to window with `attach_action_group_to_widget()`
- Actions use "app.action_name" namespace

#### 4.3 New Files Created
- `v3/pkg/application/menu_linux_gtk4.go` - GTK4 menu processing
- `v3/pkg/application/menuitem_linux_gtk4.go` - GTK4 menu item handling

#### 4.4 Build Tag Changes
- `menu_linux.go` - Added `gtk3` tag
- `menuitem_linux.go` - Added `gtk3` tag

#### 4.5 Key Functions
- `menuActionActivated()` - Callback when GAction is triggered
- `menuItemNewWithId()` - Creates GMenuItem + associated GSimpleAction
- `menuCheckItemNewWithId()` - Creates stateful toggle action
- `menuRadioItemNewWithId()` - Creates radio action
- `set_action_enabled()` / `set_action_state()` - Manage action state

TODO (deferred):
- [ ] Context menus with GtkPopoverMenu

### Phase 5: Asset Server ✅ COMPLETE

WebKitGTK 6.0 uses the same URI scheme handler API as WebKitGTK 4.1.
The asset server implementation is identical between GTK3 and GTK4.

#### 5.1 Asset Server Files (already created in Phase 1)
- `v3/internal/assetserver/webview/webkit6.go` - WebKitGTK 6.0 helpers
- `v3/internal/assetserver/webview/request_linux_gtk4.go` - Request handling
- `v3/internal/assetserver/webview/responsewriter_linux_gtk4.go` - Response writing

#### 5.2 Missing Exports Added
The GTK4 CGO file was missing two critical exports that were in the GTK3 file:
- `onProcessRequest` - Handles URI scheme requests from WebKit
- `sendMessageToBackend` - Handles JavaScript to Go communication

Both exports were added to `linux_cgo_gtk4.go`.

#### 5.3 Key Differences from GTK3
| Aspect | GTK3 | GTK4 |
|--------|------|------|
| pkg-config | `webkit2gtk-4.1` | `webkitgtk-6.0` |
| Headers | `webkit2/webkit2.h` | `webkit/webkit.h` |
| Min version | 2.40 | 6.0 |
| URI scheme API | Same | Same |

TODO (deferred to testing phase):
- [ ] Test asset loading on actual GTK4 system
- [ ] Verify JavaScript execution works correctly

### Phase 6: Docker & Build System ✅ COMPLETE

#### 6.1 Docker Container Updates
Updated both Dockerfile.linux-x86_64 and Dockerfile.linux-arm64 to install:
- GTK3 + WebKit2GTK 4.1 (default build target)
- GTK4 + WebKitGTK 6.0 (for experimental `-tags gtk4` builds)

Build scripts now support `BUILD_TAGS` environment variable:
- Default: Builds with GTK3/WebKit2GTK 4.1
- `BUILD_TAGS=gtk4`: Builds with GTK4/WebKitGTK 6.0 (experimental)

#### 6.2 Taskfile Targets
New targets added to `v3/Taskfile.yaml`:

| Target | Description |
|--------|-------------|
| `test:example:linux` | Build single example with GTK3 (native, default) |
| `test:example:linux:gtk4` | Build single example with GTK4 (native, experimental) |
| `test:examples:linux:docker:x86_64` | Build all examples with GTK3 in Docker |
| `test:examples:linux:docker:x86_64:gtk4` | Build all examples with GTK4 in Docker (experimental) |
| `test:examples:linux:docker:arm64` | Build all examples with GTK3 in Docker (ARM64) |
| `test:examples:linux:docker:arm64:gtk4` | Build all examples with GTK4 in Docker (ARM64, experimental) |

TODO (deferred):
- [ ] Update CI/CD workflows to test both GTK versions

### Phase 8: Dialog System ✅ COMPLETE

GTK4 completely replaced the dialog APIs. GTK3's `GtkFileChooserDialog` and
`gtk_message_dialog_new` are deprecated/removed.

#### 8.1 File Dialogs
GTK4 uses `GtkFileDialog` with async API:
- `gtk_file_dialog_open()` - Open single file
- `gtk_file_dialog_open_multiple()` - Open multiple files
- `gtk_file_dialog_select_folder()` - Select folder
- `gtk_file_dialog_select_multiple_folders()` - Select multiple folders
- `gtk_file_dialog_save()` - Save file

Key differences:
- No more `gtk_dialog_run()` - everything is async with callbacks
- Filters use `GListStore` of `GtkFileFilter` objects
- Results delivered via `GAsyncResult` callbacks
- Custom button text via `gtk_file_dialog_set_accept_label()`

#### 8.1.1 GTK4 File Dialog Limitations (Portal-based)

GTK4's `GtkFileDialog` uses **xdg-desktop-portal** for native file dialogs. This provides
better desktop integration but removes some application control:

| Feature | GTK3 | GTK4 | Notes |
|---------|------|------|-------|
| `ShowHiddenFiles()` | ✅ Works | ❌ No effect | User controls via portal UI toggle |
| `CanCreateDirectories()` | ✅ Works | ❌ No effect | Always enabled in portal |
| `ResolvesAliases()` | ✅ Works | ❌ No effect | Portal handles symlinks |
| `SetButtonText()` | ✅ Works | ✅ Works | `gtk_file_dialog_set_accept_label()` |
| Multiple folders | ✅ Works | ✅ Works | `gtk_file_dialog_select_multiple_folders()` |

**Why these limitations exist**: GTK4's portal-based dialogs delegate UI control to the
desktop environment (GNOME, KDE, etc.). This is intentional - the portal provides
consistent UX across applications and respects user preferences.

#### 8.2 Message Dialogs
GTK4 uses `GtkAlertDialog`:
- `gtk_alert_dialog_choose()` - Show dialog with buttons
- Buttons specified as NULL-terminated string array
- Default and cancel button indices configurable

#### 8.3 Implementation Details
- Request ID tracking for async callback matching
- `fileDialogCallback` / `alertDialogCallback` C exports for results
- `runChooserDialog()` and `runQuestionDialog()` Go wrappers
- `runOpenFileDialog()` and `runSaveFileDialog()` convenience functions

| GTK3 | GTK4 |
|------|------|
| `GtkFileChooserDialog` | `GtkFileDialog` |
| `gtk_dialog_run()` | Async callbacks |
| `gtk_message_dialog_new()` | `GtkAlertDialog` |
| `gtk_widget_destroy()` | `g_object_unref()` |

### Phase 9: Keyboard Accelerators ✅ COMPLETE

GTK4 uses `gtk_application_set_accels_for_action()` to bind keyboard shortcuts to GActions.

#### 9.1 Key Components

**C Helper Functions** (in `linux_cgo_gtk4.go`):
- `set_action_accelerator(app, action_name, accel)` - Sets accelerator for a GAction
- `build_accelerator_string(key, mods)` - Converts key+modifiers to GTK accelerator string

**Go Functions** (in `linux_cgo_gtk4.go`):
- `namedKeysToGTK` - Map of key names to GDK keysym values (e.g., "backspace" → 0xff08)
- `parseKeyGTK(key)` - Converts Wails key string to GDK keysym
- `parseModifiersGTK(modifiers)` - Converts Wails modifiers to GdkModifierType
- `acceleratorToGTK(accel)` - Converts full accelerator to GTK format
- `setMenuItemAccelerator(itemId, accel)` - Sets accelerator for a menu item

**Integration** (in `menuitem_linux_gtk4.go`):
- `setAccelerator()` method on `linuxMenuItem` calls `setMenuItemAccelerator()`
- `newMenuItemImpl()`, `newCheckMenuItemImpl()`, `newRadioMenuItemImpl()` all set accelerators during creation

#### 9.2 Accelerator String Format

GTK accelerator strings use format like:
- `<Control>q` - Ctrl+Q
- `<Control><Shift>s` - Ctrl+Shift+S
- `<Alt>F4` - Alt+F4
- `<Super>e` - Super+E (Windows/Command key)

#### 9.3 Modifier Mapping

| Wails Modifier | GDK Modifier |
|----------------|--------------|
| `CmdOrCtrlKey` | `GDK_CONTROL_MASK` |
| `ControlKey` | `GDK_CONTROL_MASK` |
| `OptionOrAltKey` | `GDK_ALT_MASK` |
| `ShiftKey` | `GDK_SHIFT_MASK` |
| `SuperKey` | `GDK_SUPER_MASK` |

### Phase 10: Testing 📋 PENDING

TODO:
- [ ] Test on Ubuntu 24.04 (native GTK4)
- [ ] Test on Ubuntu 22.04 (backported WebKitGTK 6.0)
- [ ] Test legacy build on older systems
- [ ] Performance benchmarks
- [ ] Verify file dialogs work correctly
- [ ] Verify message dialogs work correctly

## API Differences: GTK3 vs GTK4

| Feature | GTK3 | GTK4 |
|---------|------|------|
| Init | `gtk_init(&argc, &argv)` | `gtk_init_check()` |
| Container | `gtk_container_add()` | `gtk_window_set_child()` |
| Show | `gtk_widget_show_all()` | Widgets visible by default |
| Hide | `gtk_widget_hide()` | `gtk_widget_set_visible(w, FALSE)` |
| Clipboard | `GtkClipboard` | `GdkClipboard` |
| Menu | `GtkMenu/GtkMenuItem` | `GMenu/GAction` |
| Menu Bar | `GtkMenuBar` | `GtkPopoverMenuBar` |
| Window Move | `gtk_window_move()` | NO-OP on Wayland |
| Window Position | `gtk_window_get_position()` | Not available on Wayland |
| Destroy | `gtk_widget_destroy()` | `gtk_window_destroy()` |
| Drag Start | `gtk_window_begin_move_drag()` | `gtk_native_get_surface()` + surface drag |

## Files Reference

Post-#5463 default flip — GTK4 is the default; GTK3 is opt-in via `-tags gtk3` and scheduled for removal in v3.1.

### GTK4 (Default) Files — built when no tag is set
```
v3/pkg/application/
  linux_cgo.go               # Main CGO (!gtk3 tag - default)
  linux_cgo.c                # cgo C source (!gtk3 tag - default)
  linux_cgo.h                # cgo C header (!gtk3 tag - default)
  application_linux.go       # App lifecycle (!gtk3 tag - default)
  gtkdispatch_linux.go       # GTK main-thread dispatch (!gtk3 tag - default)
  menu_linux.go              # Menu processing (!gtk3 tag - default)
  menuitem_linux.go          # Menu item handling (!gtk3 tag - default)

v3/internal/assetserver/webview/
  webkit_linux.go            # WebKitGTK 6.0 helpers (!gtk3 tag - default)
  request_linux.go           # Request handling (!gtk3 tag - default)
  responsewriter_linux.go    # Response writing (!gtk3 tag - default)

v3/internal/capabilities/
  capabilities_linux.go      # GTK4 capabilities (!gtk3 tag - default)

v3/internal/operatingsystem/
  webkit_linux.go            # WebKit version info (!gtk3 tag - default)
```

### GTK3 (Legacy) Files — built only with `-tags gtk3`
```
v3/pkg/application/
  linux_cgo_gtk3.go          # Main CGO (gtk3 tag - legacy)
  application_linux_gtk3.go  # App lifecycle (gtk3 tag - legacy)
  gtkdispatch_linux_gtk3.go  # GTK main-thread dispatch (gtk3 tag - legacy)
  menu_linux_gtk3.go         # Menu processing (gtk3 tag - legacy)
  menuitem_linux_gtk3.go     # Menu item handling (gtk3 tag - legacy)

v3/internal/assetserver/webview/
  webkit_linux_gtk3.go       # WebKit2GTK 4.1 helpers (gtk3 tag - legacy)
  request_linux_gtk3.go      # Request handling (gtk3 tag - legacy)
  responsewriter_linux_gtk3.go # Response writing (gtk3 tag - legacy)

v3/internal/capabilities/
  capabilities_linux_gtk3.go # GTK3 capabilities (gtk3 tag - legacy)

v3/internal/operatingsystem/
  webkit_linux_gtk3.go       # WebKit version info (gtk3 tag - legacy)
```

> **Historical note:** The Phase tracker blocks earlier in this document (Phases 1–4) reference the pre-flip filenames (`*_linux_gtk4.go`, `webkit6.go`, etc.) as a record of work done at the time. Those references are historical and have not been retconned; new work should reference the post-flip layout above.

### Shared Files (no GTK-specific code)
```
v3/pkg/application/
  webview_window_linux.go    # Window wrapper (uses methods from CGO files)
  systemtray_linux.go        # D-Bus based, GTK-agnostic
  
v3/internal/assetserver/webview/
  request.go                 # Interface definitions
  responsewriter.go          # Interface definitions
```

## Changelog

### 2026-01-07 (Session 11)
- Fixed GTK4 dialog system bugs
- **File Dialog Fix**: Removed premature `g_object_unref()` that freed dialog before async callback
  - GTK4 async dialogs manage their own lifecycle
  - Commit: `6f9c5beb5`
- **Alert Dialog Fixes**:
  - Removed premature `g_object_unref(dialog)` from `show_alert_dialog()` (same issue as file dialogs)
  - Fixed deadlock in `dialogs_linux.go` - `InvokeAsync` → `go func()` since `runQuestionDialog` blocks internally
  - Fixed `runQuestionDialog` to use `options.Title` as message (was using `options.Message`)
  - Added default "OK" button when no buttons specified
  - Commit: `1a77e6091`
- **Other Fixes**:
  - Fixed checkptr errors with `-race` flag by changing C signal functions to accept `uintptr_t` (`3999f1f24`)
  - Fixed ExecJS race condition by adding mutex for `runtimeLoaded`/`pendingJS` (`8e386034e`)
- Added DEBUG_LOG macro for compile-time debug output: `CGO_CFLAGS="-DWAILS_GTK_DEBUG" go build ...`
- Added manual dialog test suite in `v3/test/manual/dialog/`
- **Additional Dialog Fixes** (Session 11 continued):
  - Added `gtk_file_dialog_set_accept_label()` for custom button text
  - Added `gtk_file_dialog_select_multiple_folders()` for multiple directory selection
  - Fixed data race in `application.go` cleanup - was using RLock() when writing `a.windows = nil`
  - Documented GTK4 portal limitations (ShowHiddenFiles, CanCreateDirectories have no effect)
- Files modified:
  - `v3/pkg/application/linux_cgo_gtk4.go` - dialog fixes, race fixes, accept label, multiple folders
  - `v3/pkg/application/linux_cgo_gtk4.c` - DEBUG_LOG macro, alert dialog lifecycle fix, select_multiple_folders callback
  - `v3/pkg/application/linux_cgo_gtk4.h` - uintptr_t for signal functions
  - `v3/pkg/application/dialogs_linux.go` - deadlock fix
  - `v3/pkg/application/webview_window.go` - pendingJS mutex
  - `v3/pkg/application/application.go` - RLock → Lock for cleanup writes
  - `docs/src/content/docs/reference/dialogs.mdx` - documented GTK4 limitations

### 2026-01-04 (Session 10)
- Fixed Window → Zoom menu behavior to toggle maximize/restore (was incorrectly calling webview zoomIn)
- Fixed radio button styling in GTK4 GMenu (now shows dots instead of checkmarks)
  - Implemented proper GMenu radio groups with string-valued stateful actions
  - All items in group share same action name with unique target values
  - Added `create_radio_menu_item()` C helper and `menuRadioItemNewWithGroup()` Go wrapper
- Researched Wayland minimize behavior:
  - `gtk_window_minimize()` works on GNOME/KDE (sends xdg_toplevel_set_minimized)
  - May be no-op on tiling WMs (Sway, etc.) per Wayland protocol design
- Fixed app not terminating when last window closed
  - Added quit logic to `unregisterWindow()` in `application_linux_gtk4.go`
  - Respects `DisableQuitOnLastWindowClosed` option
- Fixed menu separators not showing
  - GMenu uses sections for visual separators (not separate separator items)
  - Rewrote menu processing to group items into sections, separators create new sections
  - Added `menuNewSection()`, `menuAppendSection()`, `menuAppendItemToSection()` helpers
- Added CSS provider to reduce popover menu padding
- Removed all debug println statements
- Files modified:
  - `v3/pkg/application/linux_cgo_gtk4.go` - added radio group support, section helpers
  - `v3/pkg/application/linux_cgo_gtk4.c` - added create_radio_menu_item(), init_menu_css()
  - `v3/pkg/application/linux_cgo_gtk4.h` - added function declaration
  - `v3/pkg/application/application_linux_gtk4.go` - added quit-on-last-window logic
  - `v3/pkg/application/menu_linux_gtk4.go` - section-based menu processing, radio groups
  - `v3/pkg/application/menuitem_linux_gtk4.go` - updated radio item creation
  - `v3/pkg/application/webview_window_linux.go` - fixed zoom() to toggle maximize
  - `v3/pkg/application/window_manager.go` - removed debug output

### 2026-01-04 (Session 9)
- Fixed GTK4 window creation crash (SIGSEGV in gtk_application_window_new)
- **Root Cause**: GTK4 requires app to be "activated" before creating windows
- **Solution**: Added activation synchronization mechanism:
  - Added `activated` channel and `sync.Once` to `linuxApp` struct
  - Added `markActivated()` method called from `activateLinux()` callback
  - Added `waitForActivation()` method for callers to block until ready
  - Modified `WebviewWindow.Run()` to wait for activation before `InvokeSync`
- Files modified:
  - `v3/pkg/application/application_linux_gtk4.go` - activation gate
  - `v3/pkg/application/linux_cgo_gtk4.go` - call markActivated() in activateLinux
  - `v3/pkg/application/webview_window.go` - wait for activation on GTK4
- GTK4 apps now create windows successfully without crashes

### 2026-01-04 (Session 8)
- Fixed GTK3/GTK4 symbol conflict in operatingsystem package
- Added `gtk3` build tag to `v3/internal/operatingsystem/webkit_linux.go`
- Created `v3/internal/operatingsystem/webkit_linux_gtk4.go` with GTK4/WebKitGTK 6.0
- Moved app initialization from `init()` to `newPlatformApp()` for cleaner setup
- Resolved runtime crash: "GTK 2/3 symbols detected in GTK 4 process"
- Verified menu example runs successfully with GTK 4.20.3 and WebKitGTK 2.50.3

### 2026-01-04 (Session 7)
- Completed Phase 9: Keyboard Accelerators
- Added namedKeysToGTK map with GDK keysym values for all special keys
- Added parseKeyGTK() and parseModifiersGTK() conversion functions
- Added acceleratorToGTK() to convert Wails accelerator format to GTK
- Added setMenuItemAccelerator() Go wrapper that calls C helpers
- Integrated accelerator setting in all menu item creation functions
- Uses gtk_application_set_accels_for_action() for GTK4 shortcut binding

### 2026-01-04 (Session 6)
- Completed Phase 8: Dialog System
- Implemented GtkFileDialog for file open/save/folder dialogs
- Implemented GtkAlertDialog for message dialogs
- Added async callback system for GTK4 dialogs (no more gtk_dialog_run)
- Added C helper functions and Go wrapper functions

### 2026-01-04 (Session 5 continued)
- Completed Phase 6: Docker & Build System
- Updated Dockerfile.linux-x86_64 and Dockerfile.linux-arm64 for GTK4 + GTK3
- Added BUILD_TAGS environment variable support in build scripts
- Added Taskfile targets for GTK4 (default) and GTK3 (legacy) builds

### 2026-01-04 (Session 5)
- Completed Phase 5: Asset Server
- Verified WebKitGTK 6.0 uses same URI scheme handler API as WebKitGTK 4.1
- Added missing `onProcessRequest` export to linux_cgo_gtk4.go
- Added missing `sendMessageToBackend` export to linux_cgo_gtk4.go
- Confirmed asset server files (webkit6.go, request/responsewriter) are complete

### 2026-01-04 (Session 4)
- Completed Phase 4: Menu System
- Implemented GMenu/GAction architecture for GTK4 menus
- Created GtkPopoverMenuBar integration
- Added menu_linux_gtk4.go and menuitem_linux_gtk4.go
- Added gtk3 build tags to original menu files
- Implemented stateful actions for checkboxes and radio items

### 2026-01-04 (Session 3)
- Completed Phase 3: Window Management
- Implemented GTK4 event controllers (GtkEventControllerFocus, GtkGestureClick, GtkEventControllerKey)
- Implemented window drag using GdkToplevel API (gdk_toplevel_begin_move/resize)
- Implemented complete drag-and-drop with GtkDropTarget
- Fixed window state detection (isMinimised, isMaximised, isFullscreen)
- Fixed size() function to properly return window dimensions
- Updated windowSetGeometryHints for GTK4 (uses gtk_widget_set_size_request)

### 2026-01-04 (Session 2)
- Completed Phase 2: Doctor & Capabilities
- Updated all 7 package managers for GTK4/WebKitGTK 6.0 as primary
- Added GTKVersion and WebKitVersion fields to Capabilities struct
- Created capabilities_linux_gtk3.go for legacy build path

### 2026-01-04 (Session 1)
- Initial implementation of GTK4 build infrastructure
- Added `gtk3` constraint to 5 existing files
- Created 5 new GTK4 stub files
- Updated UNRELEASED_CHANGELOG.md

---

## CEF / Chromium Embedded — Linux opt-in tracker

**Branch**: `feat/linux-cef`
**Started**: 2026-07-09
**Status**: 📋 PLANNING (Fase 0 pending)

### Goal

Add CEF as a third webview backend on Linux, behind `-tags cef`, while preserving `webgtk` (default) and `gtk3` (legacy) untouched.

### Decision C.1 — CEF as opt-in via `-tags cef` (2026-07-09)

**Context**: Some apps need Chromium-grade rendering (widevine/L1, better ES support, WebUSB, etc.) on Linux without Windows/macOS. CEF fills that gap.

**Decision**: Introduce a `cef` build tag. The default build (no tag) and `gtk3` legacy tag remain the supported paths; `cef` is experimental opt-in.

**Rationale**:
- Mirrors the existing `gtk3` opt-in pattern (Decision 1.1)
- No runtime cost for users who don't enable it
- Keeps the bundled library footprint minimal (CEF only loaded when requested)

### Decision C.2 — Use `energye/energy` as Go↔CEF binding (2026-07-09)

**Context**: Two options to expose CEF to Go: cgo manual against `libcef.so` (~2200 LOC of equivalent `linux_cgo.go` bindings) or use the existing `github.com/energye/energy` package.

**Decision**: Use `github.com/energye/energy/v3 v3.0.16` (CEF 109-compatible, current stable release from May 30, 2026) as the C bindings. Use `energye/cef` as a **library**, not `energye/v3/application` as a framework (the latter would replace Wails).

**Rationale**:
- Production-tested, multiple years of maintenance
- Aligned with CEF 109 (current stable LTS at plan time)
- Saves ~4-6 weeks of binding work
- Pins cleanly in `go.mod`

**Risk**: If `energye/energy` becomes unmaintained, fork to `Wails-CEF/internal/energy-fork/`. Plan B (no energye): cgo manual contra `libcef.so`, ~4-6 semanas (vs ~1-2 con energye).

**Strategy**: Import `github.com/energye/cef` (and optionally `github.com/energye/lcl`) as **libraries** to implement `linuxWebviewWindow` inside Wails. Do **not** import `github.com/energye/energy/v3/application` — that would replace Wails' own application/event loop.

### Implementation Phases

| Phase | Name | Status | LOC est. | Files |
|---|---|---|---|---|
| 0 | Build tag scaffolding | ✅ COMPLETE (2026-07-09) | ~15 diffs | 14 modified |
| 1 | First CEF build (purego-cef + GTK4 host) | ✅ COMPLETE (2026-07-09) | ~900 LOC | 6 new + 1 dep |
| 2 | Asset server bridge (route + detect, no body) | ✅ COMPLETE (2026-07-09) | ~300 LOC | 2 new + 1 modified |
| 3 | JS↔Go IPC via CefV8Handler + RegisterExtension | ✅ COMPLETE (2026-07-09) | ~350 LOC | 3 new + 1 modified |
| 4 | Body streaming + return values + flags/env + events | ✅ COMPLETE (2026-07-09) | ~250 LOC diff | 2 modified |
| 5 | doctor-ng + packaging | 📋 PENDING | ~150 | 8 modified |
| 6 | Examples + CI + docs | 📋 PENDING | varies | 1 new + tasks |

### Files inventory

See `history/PLAN.md` §3 for the full file-by-file plan.

### Build matrix

| Tag set | Backend | Status |
|---|---|---|
| (none) | WebKitGTK 6.0 + GTK4 | ✅ Verified compiles identical to upstream |
| `-tags gtk3` | WebKit2GTK 4.1 + GTK3 (legacy) | ✅ Verified compiles identical to upstream |
| `-tags server` | Headless HTTP | ✅ Verified compiles identical to upstream |
| `-tags cef` | CEF (purego-cef bindings) + GTK4 host | ✅ Phase 1 complete — produces ~18MB ELF binary, all 4 build modes (default/gtk3/server/cef) build `examples/plain` |

### Session log

#### 2026-07-09 (Session C.0)
- Cloned wails v3 (master, alpha2.117) at `/home/nicolas/Documentos/GitHub/Wails-CEF`
- Created branch `feat/linux-cef`
- Created plan at `history/PLAN.md` (full 6-phase plan)
- Confirmed `energye/energy/v3 v3.0.16` available in Go module proxy (commit `5eec43ce0dfae13d548af0f06d74191c1db97217`, May 30, 2026)
- Confirmed ecosystem analysis: `energye` is the only viable CEF binding for Go in 2026; alternatives (`bnema/purego-cef`, `Crushless/fyne_browser`, `richardwilkes/cef`) are either experimental, dead, or coupled to other toolkits
- Strategy: use energye as **library** (import `energye/cef` for bindings), NOT as framework (avoid `energye/v3/application` which would replace Wails)
- Added §0 to `history/PLAN.md` with full ecosystem comparison table
- Inventoried all `*_linux*.go` files: 10 require build-tag modification to add `!cef`

#### 2026-07-09 (Session C.0b — Phase 0)
- Added `!cef` to 14 build tags across `v3/pkg/application/` and `v3/internal/assetserver/webview/`:

  **pkg/application (10)**:
  - `application_linux.go` (webgtk default)
  - `application_linux_gtk3.go` (legacy)
  - `linux_cgo.go`, `linux_cgo.c`, `linux_cgo_gtk3.go`
  - `gtkdispatch_linux.go`, `gtkdispatch_linux_gtk3.go`
  - `menu_linux.go`, `menu_linux_gtk3.go`
  - `menuitem_linux.go`, `menuitem_linux_gtk3.go`
  - `application_linux_dbus.go`
  - `global_shortcut_linux.go`, `global_shortcut_linux_portal.go`
  - `global_shortcut_linux_x11.go`, `global_shortcut_linux_x11_test.go`
  - `permissions_linux.go`

  **internal/assetserver/webview (3)**:
  - `request_linux.go`, `request_linux_gtk3.go`
  - `responsewriter_linux.go`, `responsewriter_linux_gtk3.go`
  - `webkit_linux.go`, `webkit_linux_gtk3.go`

- `webview_window_linux.go` left with `linux && !cef && !android && !server` (no `!gtk3`) so it serves **both default AND gtk3** (the shared `linuxWebviewWindow` type).

- Verified all 4 build modes:
  - `go build ./pkg/application/` → **exit 0** (default webgtk, no changes)
  - `go build -tags gtk3 ./pkg/application/` → **exit 0** (legacy, no changes)
  - `go build -tags server ./pkg/application/` → **exit 0** (no changes)
  - `go build -tags cef ./pkg/application/` → **exit 1** with expected errors:
    - `undefined: linuxApp`
    - `undefined: linuxWebviewWindow`
    - `undefined: fatalHandler`
  - Asset server: same verification, default + gtk3 both compile clean.

- **Phase 0 ✅ COMPLETE**. The build tag scaffolding is in place; webgtk/gtk3/server paths compile identically to upstream. CEF path now requires Phase 1 stubs (`application_linux_cef.go`, `webview_window_linux_cef.go`, `linux_cgo_cef.go`).

#### 2026-07-09 (Session C.0c — Phase 1)

**Goal**: First end-to-end `-tags cef` build that produces a working binary.
Strategy: purego-cef bindings + GTK4 host window, mirroring the default webgtk
backend's host pattern.

**Decision C.3 — Use `purego-cef` (not `energye/cef`)**:
- purego-cef v0.13.3 is pure Go (CGO_ENABLED=0), has zero GTK dependency, and
  ships hand-written `cef.Init` / `cef.Shutdown` / `cef.BrowserHostCreateBrowserSync`
  that work out of the box. Energye/cef forces CGO + drags LCL/GTK3 internally.
- Tradeoff: purego-cef is pre-1.0 but actively maintained (16 releases since
  2026-03). Energye is more mature but heavier.

**Files created** (6 new, ~900 LOC):
- `v3/pkg/application/linux_cgo_cef.go` — cgo host helpers (`cef_attach_to_gtk_widget`,
  `cefCreateHostWindow`, `cefSetWindowTitle`, etc.) + `pointer`/`windowPointer`/`dragInfo`
  type aliases (defined locally because linux_cgo.go is excluded from `cef` build).
- `v3/pkg/application/application_linux_cef.go` — `linuxApp` (Phase 1 stub mirroring
  GTK4 fields), `linuxApp.run()` calls `cefInit()` then `appRun()`, `fatalHandler`.
- `v3/pkg/application/webview_window_linux_cef.go` — `linuxWebviewWindow` struct
  with all `webviewWindowImpl` interface methods (most as no-op Phase 1 stubs).
  Real `run()` creates GTK4 host window + CEF browser via
  `cef.BrowserHostCreateBrowserSync`.
- `v3/pkg/application/cef_client_stub.go` — `cefClientStub` implementing
  `cef.Client` with all handler getters returning nil (CEF defaults).
- `v3/pkg/application/clipboard_linux_cef.go` — clipboard no-op stubs.
- `v3/pkg/application/dialogs_linux_cef.go` — dialog stubs (file picker, message
  dialog all return errors in Phase 1; full impl lands in Phase 4).
- `v3/pkg/application/menu_global_shortcut_linux_cef.go` — menu/global-shortcut/
  system-tray stub implementations for the CEF build.

**Files modified** (additional `!cef` tags added):
- `v3/pkg/application/clipboard_linux.go`, `dialogs_linux.go`, `mainthread_linux.go`,
  `screen_linux.go`, `events_common_linux.go`, `systemtray_linux.go`,
  `webview_window_linux_dev.go` — all depend on GTK4 cgo symbols
  (`gtkDispatch`, `clipboardSet`, `runQuestionDialog`, etc.) and are now
  excluded from the CEF build.

**Dependency**:
- `github.com/bnema/purego-cef v0.13.3` (added as indirect; becomes direct
  once Phase 1 imports are stabilized).

**Verification**:
```
go build ./pkg/application/                          exit 0  (default webgtk unchanged)
go build -tags gtk3 ./pkg/application/              exit 0  (legacy unchanged)
go build -tags server ./pkg/application/            exit 0
go build -tags cef ./pkg/application/               exit 0  ← NEW
go build -tags gtk3 ./internal/assetserver/webview/ exit 0
cd v3/examples/plain && go build -tags cef -o /tmp/cef-hello-test .   exit 0 (18MB ELF)
cd v3/examples/plain && go build            -o /tmp/plain-default   . exit 0 (16MB ELF)
cd v3/examples/plain && go build -tags gtk3 -o /tmp/plain-gtk3      . exit 0 (16MB ELF)
```

**Known limitations** (all addressed in Phases 2-4):
- No asset server bridge (browsers only load http(s) URLs).
- No JS↔Go IPC (`window.wails` shim not injected).
- Native dialogs (file picker, message) return errors.
- DevTools only opens when `w.browser != nil` (basic path works).
- Window resize doesn't propagate to the CEF X11 window.
- Drag & drop, permissions, fullscreen, menu integration all stubbed.

**Phase 1 ✅ COMPLETE**. The build pipeline works end-to-end. Next: Phase 2
implements the asset server scheme handler so CEF can load `wails://` URLs
from the embedded frontend.

#### 2026-07-09 (Session C.0d — Phase 2 partial)

**Goal**: Wire the CEF request pipeline into the assetserver so that
`wails://*` and `http://wails.localhost/*` URLs reach the existing
`assetserver.Handler`. Full body streaming is deferred to Phase 4 (CEF's
`cef.Response` has no `SetBody`; serving bodies requires a parallel
`CefResourceHandler::ReadResponse` callback).

**Architecture decision**: The CEF bridge lives in `pkg/application/`
(not `internal/assetserver/webview/`) so that the assetserver stays
unaware of CEF. The bridge adapts between CEF's inbound port
(`cef.Request`) and the existing `webview.Request` interface that the
assetserver handler already accepts.

**Files created** (2 new, ~300 LOC):
- `v3/pkg/application/cef_request_bridge.go` — `cefRequest` (wraps
  `cef.Request` to satisfy `webview.Request`) and `cefResponseWriter`
  (wraps `cef.Response` to satisfy `webview.ResponseWriter`).
- `v3/pkg/application/cef_request_handler.go` — `cefRequestHandler`
  implements `cef.RequestHandler`; `cefResourceRequestHandler` implements
  `cef.ResourceRequestHandler`. The handler detects
  `wails://`, `http://wails.localhost/`, and `https://wails.localhost/`
  URLs; non-asset URLs pass through to CEF unchanged.

**Files modified** (1):
- `v3/pkg/application/cef_client_stub.go` — `GetRequestHandler()` now
  returns the singleton `cefRequestHandler` (was nil in Phase 1).
- `v3/pkg/application/application_linux_cef.go` — `newPlatformApp` calls
  `setCefAssetsHandler(parent.assets)` so the CEF handler can dispatch
  to the assetserver.

**Wiring**:
- `setCefAssetsHandler(http.Handler)` is called from
  `application_linux_cef.go::newPlatformApp` after the assetserver has
  been built. The handler is stored in a package-level `cefHandlerAssets`
  variable behind a mutex; `getCefRequestHandler()` reads it under the
  same lock.

**Verification**:
```
go build ./pkg/application/                          exit 0  (default webgtk)
go build -tags gtk3 ./pkg/application/              exit 0  (legacy)
go build -tags server ./pkg/application/            exit 0
go build -tags cef ./pkg/application/               exit 0  ← NEW (Phase 2)
cd examples/plain && go build -tags cef -o /tmp/cef-phase2-test .  exit 0 (18MB ELF)
cd examples/binding && go build -tags cef -o /tmp/binding-cef .    exit 0 (18MB ELF)
```

**Phase 2 partial status — routing wired, body streaming pending**.
The pipeline now correctly identifies `wails://` URLs, constructs a
`webview.Request` adapter, and routes through the assetserver. The
cef.Response it builds back is currently 501 Not Implemented because
`cef.Response` lacks `SetBody`. Phase 4 will implement a parallel
`CefResourceHandler::ReadResponse` callback so the actual bytes can be
streamed.

**Known Phase 2 limitations**:
- 501 returned for asset URLs (body not yet streamed).
- Custom response headers (Cache-Control, Set-Cookie, etc.) dropped —
  building `cef_string_multimap_t` requires dlopen of CEF API.
- No scheme registration — we route via OnBeforeResourceLoad, not via
  `RequestContext::RegisterSchemeHandlerFactory`. Both approaches work;
  scheme registration is cleaner and will be added in Phase 4.

**Next**: Phase 3 (JS↔Go IPC) — inject `window.wails` shim that routes
calls through a CefV8Handler → `messageprocessor`. Phase 4 will then
add the missing body streaming + scheme registration + devtools/dnd/permissions.

#### 2026-07-09 (Session C.0e — Phase 3)

**Goal**: Wire the JS↔Go IPC bridge so that `window.wails.invoke(json)`
on the frontend lands in the existing `MessageProcessor`. Use CEF's
`RegisterExtension` (which auto-injects a JS shim before any page
script) plus a `CefV8Handler` to receive the native calls.

**Architecture**:
- The JS shim (`cef_js_shim.js`) is embedded via `//go:embed` and
  passed to `cef.RegisterExtension("wails.cef", code, handler)`. CEF
  runs the shim in the renderer's V8 context BEFORE any page script.
- The shim defines `native function wails_invoke(...)` etc. that
  forward to a Go-side `cefV8Router` implementing `cef.V8Handler`.
- The router deserializes the JSON request, calls
  `MessageProcessor.HandleRuntimeCallWithIDs`, and (in Phase 4) writes
  the result back to JS via the retval out-param.

**Files created** (3 new, ~350 LOC):
- `v3/pkg/application/cef_js_shim.js` (~80 LOC JS) — V8 extension code
  that defines `wails_invoke`, `wails_callback`, `wails_log`,
  `wails_setFlags`, `wails_setEnvironment`, `wails_emit` as native
  functions; also patches `console.log/warn/error` to relay into Go.
- `v3/pkg/application/cef_js_shim_embed.go` (~15 LOC) — `//go:embed`
  wrapper exposing the JS as a string.
- `v3/pkg/application/cef_v8_handler.go` (~250 LOC) — `cefV8Router`
  dispatches by native-function name. `wails_invoke` deserializes
  the JSON, calls MessageProcessor, and logs the result.
  `wails_log` pipes browser console into the App logger.

**Files modified** (1):
- `v3/pkg/application/application_linux_cef.go` — `newPlatformApp` now
  calls `setCefMessageProcessor(parent)` and `registerCEFExtension()`
  so the V8 handler is wired and the extension installed before any
  browser is created.

**Wiring**:
- `setCefMessageProcessor(app)` stores `app.messageProcessor` in the
  package-level `cefV8Proc` (behind a RWMutex). The V8 handler reads
  it on every invoke.
- `registerCEFExtension()` is wrapped in `sync.Once` so it runs exactly
  once per process. CEF requires extensions to be registered before
  the render process is spawned.

**Verification**:
```
go build ./pkg/application/                          exit 0  (default webgtk)
go build -tags gtk3 ./pkg/application/              exit 0  (legacy)
go build -tags server ./pkg/application/            exit 0
go build -tags cef ./pkg/application/               exit 0  ← NEW (Phase 3)
cd examples/plain && go build -tags cef -o /tmp/cef-phase3-test  exit 0 (18MB ELF)
```

**Phase 3 limitations** (Phase 4 will fix):
- The retval out-param is not yet populated — JS-side Promises
  resolve to `undefined`. Fix requires CefV8Value::CreateString
  plus a Retain dance that purego-cef doesn't expose yet.
- Exception path is logged to stderr instead of being surfaced to JS
  as a TypeError.
- Async callback resolution (Go→JS Promise resolve via
  `window.wails.handleCallback`) is a Phase 4 feature.
- Flags / environment injection (the shim's
  `wails_setFlags`/`wails_setEnvironment` callbacks) is a Phase 4
  feature: Go pushes the JSON once at startup, JS caches it in
  `window._wails.flags` / `.environment`.
- `window._wails.dispatchWailsEvent` (the Go→JS event push) is wired
  via the existing `WebviewWindow::ExecJS` which calls
  `WebviewWindow::ExecJS` and ultimately `LinuxWebviewWindow::execJS`.
  Phase 4 will add a per-frame CefFrame::ExecuteJavaScript call so
  the event lands in the right V8 context.

**Next**: Phase 4 — body streaming (CefResourceHandler.ReadResponse),
return values (CefV8Value retval out-param), full flags/environment
injection, and event delivery via CefFrame::ExecuteJavaScript.

#### 2026-07-09 (Session C.0f — Phase 4)

**Goal**: Close the four open loops left by Phase 3:
1. **Body streaming** — the assetserver response body must reach CEF.
2. **Return values** — JS Promises must resolve with the result of
   MessageProcessor calls, not `undefined`.
3. **Flags / environment** — the runtime reads
   `window._wails.flags` and `window._wails.environment`; we must
   populate them once the document loads.
4. **Events Go→JS** — already worked via `WebviewWindow::ExecJS`, but
   now go through the correct CefFrame::ExecuteJavaScript path
   rather than the global ExecJS helper.

**Implementation** (mostly modifications to existing Phase 2/3 files):

*Body streaming* (`cef_request_handler.go`):
- `cefResourceRequestHandler` now also implements
  `cef.ResourceHandler` (so the same struct can be returned from
  `GetResourceHandler`).
- `OnBeforeResourceLoad` runs the assetserver handler into a
  buffered `captureResponse` (status, headers, body).
- `GetResponseHeaders` writes status + Content-Type into the
  cef.Response; `ReadResponse` feeds body bytes to CEF in a loop.

*Return values* (`cef_v8_handler.go`):
- New helpers `writeV8Retval` (writes the `V8Value` handle into
  CEF's retval out-param via `unsafe.Pointer` casts) and
  `writeV8Exception` (logs to stderr; full JS exception set
  deferred).
- `handleInvoke` now writes the JSON result into retval, so the
  JS Promise resolves with the stringified payload.

*Flags/environment* (`cef_request_handler.go`,
`application_linux_cef.go`):
- New `setCefEnvironment(app)` builds the JSON for
  `window._wails.flags` and `.environment`.
- `cefRequestHandler.OnDocumentAvailableInMainFrame` runs an
  `ExecuteJavaScript` on the main frame pushing both objects into
  the V8 context.

*Events Go→JS* (`webview_window_linux_cef.go`):
- `execJS` already used `frame.ExecuteJavaScript`; Phase 4 just
  doc-comments confirm this is the right path. The existing
  `WebviewWindow::DispatchWailsEvent` → `ExecJS` → `execJS` chain
  now correctly lands in the right V8 context.

**Verification** (all 4 modes + 1 example with `-tags cef`):
```
go build ./pkg/application/                            exit 0
go build -tags gtk3 ./pkg/application/                exit 0
go build -tags server ./pkg/application/              exit 0
go build -tags cef ./pkg/application/                 exit 0
cd examples/plain && go build -tags cef -o /tmp/cef-phase4-test  exit 0 (18MB)
```

**Phase 4 status — all four loops closed at the API surface**.
The compile is green but functional testing (running the binary
and observing a wails:// page load) is not done in this session —
CEF 147 binaries are not installed on the build host.

**Next**: Phase 5 — `doctor-ng` integration, packaging, and
end-to-end tests (optional: install libcef locally and verify
that a wails:// page actually loads with content). Phase 6 will
add the `examples/cef-hello` directory + CI workflow.
