//go:build linux && cgo && cef && !android && !server

package application

import (
	"os"
	"strings"

	"github.com/bnema/purego-cef/cef"
)

// waylandSession is computed lazily on first call to isWaylandSession()
// and cached for the lifetime of the process. The detection is
// stable for a given PID: WAYLAND_DISPLAY is set by the Wayland
// compositor at session start and not modified after that; GDK_BACKEND
// and XDG_SESSION_TYPE are likewise stable.
//
// We deliberately don't call detectWaylandSession at package init so
// unit tests (and the WebKit/GTK3 builds that share the same
// `application` package via build tags) can override environment
// variables freely before the first call.
var (
	waylandOnce   bool
	waylandResult bool
)

// detectWaylandSession returns true when the current process is
// running inside a Wayland session. The check is the union of three
// orthogonal signals (any one is sufficient):
//
//  1. WAYLAND_DISPLAY is set — the canonical signal a Wayland
//     compositor exports to its clients. Most reliable on Linux
//     desktops; absent in headless / container environments even
//     when XDG_SESSION_TYPE=wayland.
//
//  2. XDG_SESSION_TYPE == "wayland" — set by login managers (gdm,
//     sddm, etc.) and inherited by the user's session. Survives
//     shells launched without a wayland-0 socket (e.g., `ssh` from
//     a Wayland host without ForwardX11).
//
//  3. GDK_BACKEND == "wayland" — explicit opt-in via the GDK
//     environment. We honour this even when WAYLAND_DISPLAY is
//     unset so users can force the Wayland backend in CI /
//     container setups where the socket isn't reachable.
//
// The function is safe to call concurrently — it uses a one-shot
// guard rather than sync.Once because the value is a single bool
// and we don't need to coordinate any cleanup.
func detectWaylandSession() bool {
	if waylandOnce {
		return waylandResult
	}
	waylandOnce = true
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		waylandResult = true
		return waylandResult
	}
	if strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") {
		waylandResult = true
		return waylandResult
	}
	if strings.EqualFold(os.Getenv("GDK_BACKEND"), "wayland") {
		waylandResult = true
		return waylandResult
	}
	return false
}

// resetWaylandDetection clears the cached detection result. Test-only:
// allows tests to flip WAYLAND_DISPLAY / GDK_BACKEND between cases
// without restarting the process.
func resetWaylandDetection() {
	waylandOnce = false
	waylandResult = false
}

// isOnWayland is the public name used by the rest of the CEF
// backend. Aliasing detectWaylandSession keeps the public API
// stable if we ever want to expose it (the GDK layer has its own
// gdk_display_get_default + Wayland detection).
func isOnWayland() bool {
	return detectWaylandSession()
}

// waylandRuntimeStyle returns the CEF RuntimeStyle appropriate for
// the current display server. We default to Alloy on X11 (current
// behavior — see Decision C5) and switch to Chrome on Wayland
// because the Alloy runtime cannot create a Wayland surface; it
// only understands X11.
//
// The Chrome runtime uses Ozone to talk to the underlying display
// server (X11 or Wayland), which means the same CefBrowserView /
// CefWindow plumbing works on both — but the embedding model
// differs. On X11 we XReparentWindow; on Wayland we'd need to
// either use xdg-foreign to import CEF's Wayland surface into
// GTK4's, or fall back to OSR (off-screen rendering) and paint
// CEF frames into a GTK4 drawing area. Phase 5 will pick one of
// those; for now the Chrome runtime on Wayland just means CEF
// owns its own top-level Wayland window and we leave it detached
// (see Decision C15).
func waylandRuntimeStyle() cef.RuntimeStyle {
	if isOnWayland() {
		return cef.RuntimeStyleChrome
	}
	return cef.RuntimeStyleAlloy
}