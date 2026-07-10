//go:build linux && cgo && cef && !android && !server

package application

import (
	"github.com/bnema/purego-cef/cef"
)

// cefWailsApp implements cef.App. CEF calls OnBeforeCommandLineProcessing
// once at startup, before parsing argv. We use it to inject the
// Chromium switches that make CEF usable on a forced-X11 Wayland session.
//
// Methods we don't need (GetBrowserProcessHandler, GetRenderProcessHandler,
// GetResourceBundleHandler, OnRegisterCustomSchemes) all return nil; CEF
// then falls back to the default no-op behaviour. The purego-cef binding
// inspects the return values in NewApp and only wires the callbacks we
// actually provide.
type cefWailsApp struct{}

// OnBeforeCommandLineProcessing appends switches to Chromium's command
// line before CEF parses argv. We add:
//
//   - disable-gpu: on a forced-X11 Wayland session the GPU sandbox
//     refuses to start and Chromium aborts the browser with
//     "GPU process isn't usable. Goodbye." at the first paint.
//     Disabling the GPU process entirely avoids that path; CEF
//     falls back to software rendering which is fine for the
//     assetserver-served wails:// pages the CEF backend currently
//     renders.
//
//   - runtime-style=alloy: CEF 147 defaults to the Chrome runtime,
//     which doesn't honour WindowInfo.ParentWindow the same way —
//     it expects its own top-level window. Alloy is what the
//     reference CEF GTK sample uses and matches the manual
//     reparenting model we want.
func (a *cefWailsApp) OnBeforeCommandLineProcessing(processType string, commandLine cef.CommandLine) {
	if commandLine == nil {
		return
	}
	// Force Ozone to X11 for ALL process types (browser, GPU,
	// renderer, utility). On a Wayland session, Ozone defaults to
	// wayland regardless of GDK_BACKEND, and the GPU sandbox can't
	// start under XWayland — Chromium aborts the browser with
	// "GPU process isn't usable. Goodbye." at the first paint.
	commandLine.AppendSwitchWithValue("ozone-platform", "x11")
	// Disable GPU hardware acceleration; we fall back to software
	// rasterization. Fine for the assetserver-served wails:// pages.
	commandLine.AppendSwitch("disable-gpu")
	// Run the GPU process in-process. Without this, Chromium tries
	// to spawn a separate GPU subprocess and crashes with
	// "GPU process isn't usable" because the GPU sandbox can't
	// initialise on XWayland.
	commandLine.AppendSwitch("in-process-gpu")
	commandLine.AppendSwitchWithValue("runtime-style", "alloy")
}

func (a *cefWailsApp) OnRegisterCustomSchemes(cef.SchemeRegistrar)        {}
func (a *cefWailsApp) GetResourceBundleHandler() cef.ResourceBundleHandler { return nil }
func (a *cefWailsApp) GetBrowserProcessHandler() cef.BrowserProcessHandler { return nil }
func (a *cefWailsApp) GetRenderProcessHandler() cef.RenderProcessHandler  { return nil }