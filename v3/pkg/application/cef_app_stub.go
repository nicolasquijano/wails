//go:build linux && cgo && cef && !android && !server

package application

import (
	"io"
	"os"
	"path/filepath"

	"github.com/bnema/purego-cef/cef"
)

// cefResourcesDir returns the path to the CEF Resources directory,
// mirroring the logic in purego-cef's loader.resolveDir().
func cefDir() string {
	if env := os.Getenv("CEF_DIR"); env != "" {
		return env
	}
	if _, err := os.Stat("/usr/lib/cef/libcef.so"); err == nil {
		return "/usr/lib/cef"
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if _, err := os.Stat(filepath.Join(home, ".local", "share", "cef", "libcef.so")); err == nil {
			return filepath.Join(home, ".local", "share", "cef")
		}
	}
	return ""
}

func cefResourcesDir() string {
	if dir := cefDir(); dir != "" {
		return filepath.Join(dir, "Resources")
	}
	return ""
}

// cefEnsureFiles copies required CEF data files that CEF's internal
// path resolution may not find. Called once during cefInit.
func cefEnsureFiles() {
	dir := cefDir()
	if dir == "" {
		return
	}
	resDir := filepath.Join(dir, "Resources")
	releaseDir := filepath.Join(dir, "Release")

	// v8_context_snapshot.bin must exist in Resources/ for
	// PathService::Get(5) + "v8_context_snapshot.bin" to succeed.
	srcV8 := filepath.Join(releaseDir, "v8_context_snapshot.bin")
	dstV8 := filepath.Join(resDir, "v8_context_snapshot.bin")
	if _, err := os.Stat(srcV8); err == nil {
		if _, err := os.Stat(dstV8); os.IsNotExist(err) {
			if err := copyFile(srcV8, dstV8); err != nil {
				debugLog("[cefEnsureFiles] copy v8 snapshot: %v", err)
			} else {
				debugLog("[cefEnsureFiles] copied v8_context_snapshot.bin to Resources/")
			}
		}
	}

	// icudtl.dat must be next to libcef.so (CEF issue #3778).
	// CEF resolves the libcef.so path at runtime via FILE_GetModulePath,
	// which returns the resolved path (Release/) for a symlink. However
	// the CEF_DIR env var may point to the symlink parent. Copy to both.
	srcICU := filepath.Join(resDir, "icudtl.dat")
	for _, candidate := range []string{dir, filepath.Join(dir, "Release")} {
		dstICU := filepath.Join(candidate, "icudtl.dat")
		if _, err := os.Stat(srcICU); err == nil {
			if _, err := os.Stat(dstICU); os.IsNotExist(err) {
				if err := copyFile(srcICU, dstICU); err != nil {
					debugLog("[cefEnsureFiles] copy icudtl to %s: %v", candidate, err)
				} else {
					debugLog("[cefEnsureFiles] copied icudtl.dat to %s", candidate)
				}
			}
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// cefWailsApp implements cef.App. CEF calls OnBeforeCommandLineProcessing
// once at startup, before parsing argv. We use it to inject the
// Chromium switches that make CEF usable on the current display server.
//
// Methods we don't need (GetBrowserProcessHandler, GetRenderProcessHandler,
// GetResourceBundleHandler) all return nil; CEF then falls back to the
// default no-op behaviour. OnRegisterCustomSchemes IS needed — without it
// the Chrome/Alloy runtime rejects "wails://" URLs as unknown schemes.
type cefWailsApp struct{}

// OnBeforeCommandLineProcessing appends switches to Chromium's command
// line before CEF parses argv.
//
// The X11/Wayland split is now driven by isOnWayland() (see
// Decision C15 + cef_wayland_linux.go):
//
// X11 (default, current behavior preserved):
//
//   - ozone-platform=x11: CEF must create an X11 window that we can
//     reparent into the GTK4 host window. Wayland does not support
//     foreign-window embedding (no XReparentWindow equivalent).
//
//   - runtime-style=alloy: CEF 147 defaults to the Chrome runtime,
//     which ignores WindowInfo.ParentWindow and always creates a
//     fully-featured top-level window. The Alloy runtime matches the
//     reference CEF GTK embedding sample and lets us reparent the
//     browser view into our GtkBox.
//
// Wayland (Phase 5 / opt-in):
//
//   - ozone-platform=wayland: CEF talks to the compositor via
//     Ozone/Wayland. We can't XReparentWindow into a Wayland
//     surface, so CEF owns its own top-level window and the GTK4
//     host becomes a placeholder.
//
//   - runtime-style=chrome: required for Wayland. The Alloy runtime
//     does not have a Wayland surface implementation.
//
//   - --enable-features=UseOzonePlatform: required so the runtime-style
//     flag actually selects Ozone.
//
// Single-process + disable-gpu are unconditional: they're orthogonal
// to the display server (forced by the Go-runtime / fork limitation,
// see Decision C1).
func (a *cefWailsApp) OnBeforeCommandLineProcessing(processType string, commandLine cef.CommandLine) {
	if commandLine == nil {
		return
	}
	commandLine.AppendSwitchWithValue("ozone-platform", "x11")
	commandLine.AppendSwitchWithValue("runtime-style", "alloy")
	commandLine.AppendSwitch("disable-gpu")
	commandLine.AppendSwitch("in-process-gpu")
	commandLine.AppendSwitch("single-process")
	commandLine.AppendSwitchWithValue("lang", "en-US")
	commandLine.AppendSwitchWithValue("remote-debugging-port", "9999")

	if resourcesDir := cefResourcesDir(); resourcesDir != "" {
		commandLine.AppendSwitchWithValue("resources-dir-path", resourcesDir)
		commandLine.AppendSwitchWithValue("locales-dir-path", filepath.Join(resourcesDir, "locales"))
	}
}

// Scheme option flags matching CEF's cef_types.h. purego-cef does not
// export named constants, so we define them inline.
const (
	cefSchemeOptionStandard        = 0x01
	cefSchemeOptionLocal           = 0x02
	cefSchemeOptionDisplayIsolated = 0x04
	cefSchemeOptionSecure          = 0x08
	cefSchemeOptionCORSEnabled     = 0x10
	cefSchemeOptionFetchEnabled    = 0x40
)

// OnRegisterCustomSchemes registers the "wails" custom scheme so that the
// Chrome/Alloy runtime recognises wails:// URLs. Without this registration
// CEF rejects the scheme at URL-parse time and navigation silently fails.
func (a *cefWailsApp) OnRegisterCustomSchemes(registrar cef.SchemeRegistrar) {
	if registrar == nil {
		return
	}
	options := cefSchemeOptionStandard |
		cefSchemeOptionCORSEnabled |
		cefSchemeOptionFetchEnabled |
		cefSchemeOptionSecure
	registrar.AddCustomScheme("wails", int32(options))
}
func (a *cefWailsApp) GetResourceBundleHandler() cef.ResourceBundleHandler { return nil }
func (a *cefWailsApp) GetBrowserProcessHandler() cef.BrowserProcessHandler { return nil }
func (a *cefWailsApp) GetRenderProcessHandler() cef.RenderProcessHandler  { return nil }