//go:build linux && cgo && cef && !android && !server

package application

import (
	"testing"
)

// TestDetectWaylandSession covers the union-of-signals logic in
// detectWaylandSession. Each case mutates a single env var and
// verifies that var alone is enough to flip the result.
func TestDetectWaylandSession(t *testing.T) {
	cases := []struct {
		name      string
		wayland   string // value for WAYLAND_DISPLAY, "" = unset
		xdg       string // value for XDG_SESSION_TYPE, "" = unset
		gdk       string // value for GDK_BACKEND, "" = unset
		wantResult bool
	}{
		{"all-empty", "", "", "", false},
		{"wayland-display-set", "wayland-0", "", "", true},
		{"xdg-session-type-set", "", "wayland", "", true},
		{"gdk-backend-set", "", "", "wayland", true},
		{"xdg-session-type-case-insensitive", "", "Wayland", "", true},
		{"gdk-backend-case-insensitive", "", "", "Wayland", true},
		{"x11-fallback", "", "x11", "x11", false},
		{"wayland-wins-over-x11", "wayland-0", "x11", "x11", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Reset env + cache so the case is isolated.
			t.Setenv("WAYLAND_DISPLAY", c.wayland)
			t.Setenv("XDG_SESSION_TYPE", c.xdg)
			t.Setenv("GDK_BACKEND", c.gdk)
			resetWaylandDetection()

			got := detectWaylandSession()
			if got != c.wantResult {
				t.Errorf("detectWaylandSession() = %v, want %v", got, c.wantResult)
			}
		})
	}
}

// TestIsOnWaylandCached verifies the result is stable across
// calls within a single test (the function caches the first result
// for the lifetime of the process). If a test mutates env vars
// without calling resetWaylandDetection, subsequent calls must
// return the cached value.
func TestIsOnWaylandCached(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	resetWaylandDetection()

	first := isOnWayland()
	if !first {
		t.Fatal("first isOnWayland() = false, want true (env WAYLAND_DISPLAY=wayland-0)")
	}
	// Mutate env after the cache was populated. The cached result
	// must NOT flip — that's the contract.
	t.Setenv("WAYLAND_DISPLAY", "")
	if isOnWayland() != first {
		t.Errorf("isOnWayland() changed after env mutation: first=%v, second=%v", first, isOnWayland())
	}
}

// TestResetWaylandDetection verifies that resetWaylandDetection
// forces a fresh read on the next call. Important for tests that
// flip env vars between cases — without this, one case's env leaks
// into the next.
//
// The two phases are split into subtests so each subtest gets a
// clean env from t.Setenv (which restores on subtest exit, not
// test exit). Note: the test machine itself may have WAYLAND_DISPLAY
// and/or XDG_SESSION_TYPE set in the parent env (CI runners, dev
// workstations), so the "not Wayland" subtest must explicitly unset
// every signal detectWaylandSession reads.
func TestResetWaylandDetection(t *testing.T) {
	t.Run("first-read-wayland", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		t.Setenv("XDG_SESSION_TYPE", "wayland")
		t.Setenv("GDK_BACKEND", "wayland")
		resetWaylandDetection()
		if !detectWaylandSession() {
			t.Fatal("expected true with all Wayland env vars set")
		}
	})
	t.Run("second-read-after-reset", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("XDG_SESSION_TYPE", "")
		t.Setenv("GDK_BACKEND", "")
		resetWaylandDetection()
		if detectWaylandSession() {
			t.Fatal("expected false after resetting every Wayland env var to empty")
		}
	})
}

// TestWaylandRuntimeStyle verifies the runtime-style selector picks
// Chrome on Wayland, Alloy on X11. The selector must not cache
// (unlike isOnWayland); it's recomputed each call so apps can
// override WAYLAND_DISPLAY mid-process (e.g., test setups).
func TestWaylandRuntimeStyle(t *testing.T) {
	// Subtest 1: all Wayland env vars set → Chrome runtime
	t.Run("wayland", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		t.Setenv("XDG_SESSION_TYPE", "wayland")
		t.Setenv("GDK_BACKEND", "wayland")
		resetWaylandDetection()
		if got := waylandRuntimeStyle(); got != int32(1) {
			t.Errorf("waylandRuntimeStyle() = %d, want 1 (Chrome)", got)
		}
	})
	// Subtest 2: all Wayland env vars cleared → Alloy runtime.
	// Must clear every signal because the parent test env may
	// already have WAYLAND_DISPLAY / XDG_SESSION_TYPE set on a real
	// Wayland workstation.
	t.Run("x11", func(t *testing.T) {
		t.Setenv("WAYLAND_DISPLAY", "")
		t.Setenv("XDG_SESSION_TYPE", "")
		t.Setenv("GDK_BACKEND", "")
		resetWaylandDetection()
		if got := waylandRuntimeStyle(); got != int32(2) {
			t.Errorf("waylandRuntimeStyle() = %d, want 2 (Alloy)", got)
		}
	})
}