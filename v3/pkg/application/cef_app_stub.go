//go:build linux && cgo && cef && !android && !server

package application

import (
	"os"
	"path/filepath"

	"github.com/bnema/purego-cef/cef"
)

// cefResourcesDir returns the path to the CEF Resources directory,
// mirroring the logic in purego-cef's loader.resolveDir().
func cefResourcesDir() string {
	dir := ""
	if env := os.Getenv("CEF_DIR"); env != "" {
		dir = env
	} else if _, err := os.Stat("/usr/lib/cef/libcef.so"); err == nil {
		dir = "/usr/lib/cef"
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, ".local", "share", "cef")
		}
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "Resources")
}

// cefWailsApp implements cef.App. CEF calls OnBeforeCommandLineProcessing
// once at startup, before parsing argv. We use it to inject the
// Chromium switches that make CEF usable on a forced-X11 Wayland session.
//
// Methods we don't need (GetBrowserProcessHandler, GetRenderProcessHandler,
// GetResourceBundleHandler) all return nil; CEF then falls back to the
// default no-op behaviour. OnRegisterCustomSchemes IS needed — without it
// the Chrome/Alloy runtime rejects "wails://" URLs as unknown schemes.
type cefWailsApp struct{}

// OnBeforeCommandLineProcessing appends switches to Chromium's command
// line before CEF parses argv.
//
// Key switches:
//
//   - ozone-platform=x11: CEF must create an X11 window that we can
//     reparent into the GTK4 host window. Wayland does not support
//     foreign-window embedding (no XReparentWindow equivalent).
//
//   - disable-gpu: On a forced-X11 Wayland session the GPU sandbox
//     refuses to start and Chromium aborts with "GPU process isn't
//     usable. Goodbye." at the first paint. Disabling GPU makes CEF
//     fall back to the Skia software rasterizer, which is fine for
//     the assetserver-served wails:// pages.
//
//     NOTE: --disable-software-rasterizer would ALSO disable the Skia
//     fallback, leaving NO rendering pipeline → black screen.
//
//   - runtime-style=alloy: CEF 147 defaults to the Chrome runtime,
//     which ignores WindowInfo.ParentWindow and always creates a
//     fully-featured top-level window. The Alloy runtime matches the
//     reference CEF GTK embedding sample and lets us reparent the
//     browser view into our GtkBox.
func (a *cefWailsApp) OnBeforeCommandLineProcessing(processType string, commandLine cef.CommandLine) {
	if commandLine == nil {
		return
	}
	commandLine.AppendSwitchWithValue("ozone-platform", "x11")
	commandLine.AppendSwitch("disable-gpu")
	commandLine.AppendSwitch("in-process-gpu")
	commandLine.AppendSwitch("single-process")
	commandLine.AppendSwitchWithValue("lang", "en-US")
	commandLine.AppendSwitchWithValue("runtime-style", "alloy")
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
		cefSchemeOptionLocal |
		cefSchemeOptionDisplayIsolated |
		cefSchemeOptionCORSEnabled |
		cefSchemeOptionFetchEnabled
	registrar.AddCustomScheme("wails", int32(options))
}
func (a *cefWailsApp) GetResourceBundleHandler() cef.ResourceBundleHandler { return nil }
func (a *cefWailsApp) GetBrowserProcessHandler() cef.BrowserProcessHandler { return nil }
func (a *cefWailsApp) GetRenderProcessHandler() cef.RenderProcessHandler  { return nil }