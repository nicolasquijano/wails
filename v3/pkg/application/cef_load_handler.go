//go:build linux && cgo && cef && !android && !server

package application

import (
	"fmt"
	"os"

	"github.com/bnema/purego-cef/cef"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// cefLoadHandler implements cef.LoadHandler and signals the Go side when
// the main frame finishes loading. This is used instead of the JS-side
// "wails:runtime:ready" invoke message because CEF's V8 extension sets
// window.wails.invoke but the runtime JS module later overwrites
// window.wails with its own namespace object, losing the native invoke
// function. Hooking OnLoadEnd on the C++ side is more reliable.
//
// See: cef_js_shim.js (V8 RegisterExtension sets window.wails.invoke)
//      runtime.debug.js line 1622 (window.wails = index_exports)
type cefLoadHandler struct {
	w *linuxWebviewWindow
}

func getCefLoadHandler(w *linuxWebviewWindow) cef.LoadHandler {
	return &cefLoadHandler{w: w}
}

func (h *cefLoadHandler) OnLoadingStateChange(browser cef.Browser, isLoading int32, canGoBack int32, canGoForward int32) {
	if h.w == nil || h.w.parent == nil {
		return
	}
	// Translate to events.Linux.WindowLoadStarted / WindowLoadFinished
	// via the existing WindowLoadFinished-style state changes. The
	// WebKit backend uses GTK4's load-changed signal for this; for
	// CEF we only emit on the transitions that map cleanly.
	var ev uint
	if isLoading != 0 {
		ev = uint(events.Linux.WindowLoadStarted)
	} else {
		ev = uint(events.Linux.WindowLoadFinished)
	}
	windowEvents <- &windowEvent{
		WindowID: h.w.parent.id,
		EventID:  ev,
	}
}

func (h *cefLoadHandler) OnLoadStart(browser cef.Browser, frame cef.Frame, transitionType cef.TransitionType) {
	if !frame.IsMain() || h.w == nil || h.w.parent == nil {
		return
	}
	windowEvents <- &windowEvent{
		WindowID: h.w.parent.id,
		EventID:  uint(events.Linux.WindowLoadStarted),
	}
}

func (h *cefLoadHandler) OnLoadEnd(browser cef.Browser, frame cef.Frame, httpStatusCode int32) {
	if !frame.IsMain() {
		return
	}
	fmt.Fprintf(os.Stderr, "wails/cef: OnLoadEnd status=%d\n", httpStatusCode)
	if h.w == nil || h.w.parent == nil {
		return
	}
	// Emit Linux.WindowLoadFinished on the window event channel. The
	// WebKit backend fires this from WebKitWebView::load-changed; we
	// fire it from CEF's LoadHandler::OnLoadEnd which is the
	// equivalent point in the CEF pipeline.
	windowEvents <- &windowEvent{
		WindowID: h.w.parent.id,
		EventID:  uint(events.Linux.WindowLoadFinished),
	}
	// Schedule the ready signal on the main thread via InvokeAsync.
	// OnLoadEnd runs on CEF's UI thread (which is the main thread in
	// single-process mode), so calling InvokeSync here would deadlock.
	InvokeAsync(func() {
		if h.w != nil && h.w.parent != nil && !h.w.parent.isDestroyed() {
			h.w.parent.HandleMessage("wails:runtime:ready")
		}
	})
}

func (h *cefLoadHandler) OnLoadError(browser cef.Browser, frame cef.Frame, errorCode cef.Errorcode, errorText string, failedURL string) {
	if !frame.IsMain() {
		return
	}
	fmt.Fprintf(os.Stderr, "wails/cef: OnLoadError url=%q code=%v text=%q\n", failedURL, errorCode, errorText)
}
