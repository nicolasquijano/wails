//go:build linux && cgo && cef && !android && !server

package application

import (
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
)

// osrFrame is the Phase 6 OSR frame buffer. The X11 reparenting path
// never touches it; it's defined here so the struct layout is stable
// across the CEF backend regardless of which render path is in use.
type osrFrame struct {
	width, height int
	painted       bool
	buffer        []byte
}

// osrResizeMsg mirrors the C-side osrResizeMsg struct in
// linux_cgo_cef.go. Defined here so the linuxWebviewWindow field
// `osrResizeCh chan osrResizeMsg` compiles. Phase 6 wires the channel
// to the g_idle_add_full bridge.
type osrResizeMsg struct {
	w             uintptr
	width, height int
}

// linuxWebviewWindow is the CEF-flavoured webview window for Phase 1.
//
// In Phase 1 the struct fields mirror the GTK4 default, but the semantics
// differ:
//   - window is the host GtkApplicationWindow (managed by GTK4).
//   - vbox is the GtkBox inside that window (host container).
//   - webview is the X11 window ID returned by CEF for the browser view,
//     reparented into `vbox` via cefAttachToGTKWidget().
//
// The CefBrowser handle is stored on the `browser` field and used by
// execJS / openDevTools / print / etc.
//
// In Phase 2 we will add:
//   - scheme handler bridge to assetserver.Handler
//   - CefV8Handler for JS↔Go IPC
//   - load-end event emitter to wails events bus
type linuxWebviewWindow struct {
	id            uint
	application   pointer
	window        pointer
	webview       pointer // X11 window ID (uintptr) of the CEF view
	vbox          pointer // GtkBox that hosts the CEF view
	parent        *WebviewWindow
	menubar       pointer
	accels        pointer
	lastWidth     int
	lastHeight    int
	drag          dragInfo
	lastX, lastY  int
	gtkmenu       pointer
	ctxMenuOpened bool

	// CEF-specific fields
	browser cef.Browser // CEF browser handle; nil until first create

	// dragSlot holds the CEF DragData captured by the most recent
	// OnDragEnter call. The JS drop-event handler reads it via the
	// wails_cefResolveDrop native function (see cef_drag_handler.go
	// and cef_js_shim.js).
	dragSlot dragDataSlot

	// OSR (off-screen rendering) state. Populated only on Wayland
	// sessions where CEF can't embed inside the GTK4 window via
	// XReparentWindow. The drawingArea is a GtkDrawingArea that
	// blits osrFrame.buffer onto the GTK window on every paint cycle.
	// Phase 6 OSR work populates these; until then they sit at zero
	// values so the struct layout is stable.
	osrFrameMu    sync.RWMutex
	osrFrame      osrFrame
	drawingArea   unsafe.Pointer
	osrResizeCh   chan osrResizeMsg
	osrResizeOnce sync.Once

	moveDebouncer     func(func())
	resizeDebouncer   func(func())
	ignoreMouseEvents bool
}

func newWindowImpl(parent *WebviewWindow) *linuxWebviewWindow {
	result := &linuxWebviewWindow{
		application: getNativeApplication().application,
		parent:      parent,
	}
	return result
}

// setTitle is a Phase 1 stub. The GTK4 default uses gtk_window_set_title
// via CGo. We delegate to the GTK4 host window since CEF renders inside it.
func (w *linuxWebviewWindow) setTitle(title string) {
	// Delegates to CGo; uses same GTK4 call as the default backend.
	cefSetWindowTitle(w.window, title)
}

// destroy closes the CEF browser and GTK window.
//
// Order matters: CEF browser must be closed BEFORE the GTK window is
// destroyed, otherwise CEF will try to paint into a destroyed X11 window.
func (w *linuxWebviewWindow) destroy() {
	if w.browser != nil {
		// Remove the browser→window mapping before closing the
		// browser; once CEF's BrowserHost is destroyed any pending
		// JS drop handler that calls wails_cefResolveDrop would
		// otherwise race with the CEF teardown.
		unregisterCefBrowser(w.browser.GetIdentifier())
		w.browser = nil
	}
	cefDestroyWindow(w.window)
}

func (w *linuxWebviewWindow) close() {
	cefCloseWindow(w.window)
}

// run is the main entrypoint for a CEF window. In Phase 1 this creates a
// GTK4 application window with a GtkBox, then attaches a CEF browser to it.
//
// TODO(Phase 2): integrate asset server scheme handler
// TODO(Phase 3): inject JS shim for wailsIPC
func (w *linuxWebviewWindow) run() {
	debugLog("[linuxWebviewWindow.run] starting url=%q", w.parent.options.URL)
	fmt.Fprintf(os.Stderr, "wails/cef: linuxWebviewWindow.run() starting url=%q\n", w.parent.options.URL)

	app := getNativeApplication()

	// 1. Create host GTK window (GtkApplicationWindow + GtkBox).
	w.window, w.vbox = cefCreateHostWindow(
		app.application,
		w.parent.id,
	)
	debugLog("[linuxWebviewWindow.run] cefCreateHostWindow window=%v vbox=%v", w.window != nil, w.vbox != nil)
	fmt.Fprintf(os.Stderr, "wails/cef: cefCreateHostWindow returned window=%v vbox=%v\n", w.window != nil, w.vbox != nil)

	// 2. Register window in app's window map.
	app.registerWindow(w.window, w.parent.id)
	debugLog("[run] after registerWindow")

	// 3. Apply options (title, size, frameless, etc.).
	title := w.parent.options.Title
	if title == "" {
		title = w.parent.options.Name
	}
	debugLog("[run] before setTitle(%q) window=%v", title, w.window != nil)
	w.setTitle(title)
	debugLog("[run] after setTitle")
	w.setDefaultSize(w.parent.options.Width, w.parent.options.Height)
	debugLog("[run] after setDefaultSize")
	w.setSize(w.parent.options.Width, w.parent.options.Height)
	debugLog("[run] after setSize")
	if w.parent.options.BackgroundType != BackgroundTypeSolid {
		w.setTransparent()
		w.setBackgroundColour(w.parent.options.BackgroundColour)
	}
	w.setFrameless(w.parent.options.Frameless)
	w.setResizable(!w.parent.options.DisableResize)
	w.setAlwaysOnTop(w.parent.options.AlwaysOnTop)

	debugLog("[linuxWebviewWindow.run] about to call cefCreateBrowserInWidget vbox=%v url=%q", w.vbox != nil, w.parent.options.URL)

	// 4. Create CEF browser attached to the GtkBox. Pass the window
	// dimensions so the CEF view starts at the right size instead of
	// the hardcoded 800x600.
	ww := w.parent.options.Width
	wh := w.parent.options.Height
	if ww <= 0 {
		ww = 800
	}
	if wh <= 0 {
		wh = 600
	}
	w.browser = cefCreateBrowserInWidget(
		unsafe.Pointer(w.window),
		unsafe.Pointer(w.vbox),
		w.parent.options.URL,
		ww, wh,
		w,
	)
	debugLog("[linuxWebviewWindow.run] cefCreateBrowserInWidget returned browser=%v", w.browser != nil)

	// 5. Show the GTK window.
	w.show()
	debugLog("[linuxWebviewWindow.run] after show")
}

func (w *linuxWebviewWindow) show() {
	cefShowWindow(w.window)
}

func (w *linuxWebviewWindow) hide() {
	cefHideWindow(w.window)
}

func (w *linuxWebviewWindow) focus() {
	cefPresentWindow(w.window)
}

func (w *linuxWebviewWindow) forceReload() {
	if w.browser == nil {
		return
	}
	w.browser.Reload()
}

// execJS executes JavaScript in the browser's main frame. This is
// the primary Go→JS bridge for one-off script execution (used by
// wails.WebviewWindow.ExecJS, wails.DispatchWailsEvent, etc.).
//
// Phase 4: routes via CefFrame::ExecuteJavaScript so the script runs
// in the right V8 context. The empty scriptURL means the script
// doesn't appear in DevTools' source list.
func (w *linuxWebviewWindow) execJS(js string) {
	if w.browser == nil {
		return
	}
	frame := w.browser.GetMainFrame()
	if frame == nil {
		return
	}
	frame.ExecuteJavaScript(js, "", 0)
}

// openDevTools opens the Chromium DevTools window via CEF.
//
// In Phase 1 this is a no-op when the browser is not yet created.
func (w *linuxWebviewWindow) openDevTools() {
	if w.browser == nil {
		return
	}
	host := w.browser.GetHost()
	if host == nil {
		return
	}
	wi := cef.NewWindowInfo()
	settings := cef.NewBrowserSettings()
	host.ShowDevTools(&wi, nil, &settings, nil)
}

// print is a Phase 1 stub; full printing pipeline lands in Phase 4.
func (w *linuxWebviewWindow) print() error {
	if w.browser == nil {
		return fmt.Errorf("wails/cef: print: browser not ready")
	}
	host := w.browser.GetHost()
	if host == nil {
		return fmt.Errorf("wails/cef: print: no host")
	}
	host.Print()
	return nil
}

// Phase 1: stub implementations for the rest of the platformWindow interface.
// These will be filled out in Phase 4 (devtools/permisos/DnD/menu).

func (w *linuxWebviewWindow) endDrag(button uint, x, y int)             {}
func (w *linuxWebviewWindow) connectSignals() {
	// Signals are wired in cefCreateHostWindow via
	// cef_install_window_signal_handlers (called from CGo preamble).
	// This Go method exists for interface compatibility with WebKit.
}
func (w *linuxWebviewWindow) openContextMenu(menu *Menu, data *ContextMenuData) {}
func (w *linuxWebviewWindow) isNormal() bool                             { return !w.isMinimised() && !w.isMaximised() && !w.isFullscreen() }
func (w *linuxWebviewWindow) setCloseButtonEnabled(enabled bool)        {}
func (w *linuxWebviewWindow) setMinimiseButtonEnabled(enabled bool)     {}
func (w *linuxWebviewWindow) setMaximiseButtonEnabled(enabled bool)      {}
func (w *linuxWebviewWindow) disableSizeConstraints()                   {}
func (w *linuxWebviewWindow) enableSizeConstraints()                    {}
func (w *linuxWebviewWindow) on(eventID uint)                            {}
func (w *linuxWebviewWindow) zoom() {
	if w.isMaximised() {
		w.unmaximise()
	} else {
		w.maximise()
	}
}
func (w *linuxWebviewWindow) setMinMaxSize(minWidth, minHeight, maxWidth, maxHeight int) {
	if minWidth > 0 && minHeight > 0 {
		cefSetSizeRequest(w.window, minWidth, minHeight)
	}
	if maxWidth > 0 || maxHeight > 0 {
		cefSetMaxSize(w.window, maxWidth, maxHeight)
	}
}
func (w *linuxWebviewWindow) setMinSize(width, height int) {
	if width > 0 && height > 0 {
		cefSetSizeRequest(w.window, width, height)
	}
}
func (w *linuxWebviewWindow) setMaxSize(width, height int) {
	if width > 0 || height > 0 {
		cefSetMaxSize(w.window, width, height)
	}
}
func (w *linuxWebviewWindow) bounds() Rect {
	cw, ch := cefGetDefaultSize(w.window)
	cx, cy := cefGetWindowPosition(w.window)
	return Rect{X: cx, Y: cy, Width: cw, Height: ch}
}
func (w *linuxWebviewWindow) print2() error { return w.print() }
func (w *linuxWebviewWindow) width() int {
	if w.window == nil {
		return w.lastWidth
	}
	return cefWidgetWidth(w.window)
}
func (w *linuxWebviewWindow) height() int {
	if w.window == nil {
		return w.lastHeight
	}
	return cefWidgetHeight(w.window)
}
func (w *linuxWebviewWindow) getBorderSizes() *LRTB                     { return &LRTB{} }
func (w *linuxWebviewWindow) setRelativePosition(x int, y int)         { w.move(x, y) }
func (w *linuxWebviewWindow) applyScreenPlacement()                     {}
func (w *linuxWebviewWindow) setMenu(menu *Menu)                        {}
func (w *linuxWebviewWindow) nativeWindow() unsafe.Pointer              { return unsafe.Pointer(w.window) }
func (w *linuxWebviewWindow) startDrag() error                         { return nil }
func (w *linuxWebviewWindow) startResize(_ string) error              { return nil }
func (w *linuxWebviewWindow) attachModal(modalWindow *WebviewWindow)   {}
func (w *linuxWebviewWindow) setMinimiseButtonState(state ButtonState)  {}
func (w *linuxWebviewWindow) setMaximiseButtonState(state ButtonState)  {}
func (w *linuxWebviewWindow) setCloseButtonState(state ButtonState)     {}
func (w *linuxWebviewWindow) setFullscreenButtonState(state ButtonState) {}
func (w *linuxWebviewWindow) isIgnoreMouseEvents() bool                 { return w.ignoreMouseEvents }
func (w *linuxWebviewWindow) setIgnoreMouseEvents(ignore bool)          { w.ignoreMouseEvents = ignore }
func (w *linuxWebviewWindow) showMenuBar()                              {}
func (w *linuxWebviewWindow) hideMenuBar()                              {}
func (w *linuxWebviewWindow) toggleMenuBar()                            {}
func (w *linuxWebviewWindow) snapAssist()                               {}
func (w *linuxWebviewWindow) setContentProtection(enabled bool)         {}
func (w *linuxWebviewWindow) setNonClientHitTestRegions([]nonClientHitTestRegion) {
}

// copy is a Phase 1 stub. Real implementation lands in Phase 4 (called
// when the same WebviewWindow struct is reused after a destroy+recreate).
func (w *linuxWebviewWindow) copy() {
	// No-op for Phase 1.
}

// setSize resizes the GTK host window. Used by window option
// setters that change the client area after creation.
func (w *linuxWebviewWindow) setSize(width, height int) {
	if w.window == nil || width <= 0 || height <= 0 {
		return
	}
	cefSetWindowSize(w.window, width, height)
}

// setURL is a Phase 1 stub. Real implementation in Phase 2+ uses
// CefBrowser::MainFrame().LoadURL().
func (w *linuxWebviewWindow) setURL(url string) {
	if w.browser == nil || url == "" {
		return
	}
	frame := w.browser.GetMainFrame()
	if frame == nil {
		return
	}
	frame.LoadURL(url)
}

// setFrameless sets or removes window decorations.
func (w *linuxWebviewWindow) setFrameless(frameless bool) {
	cefSetDecorated(w.window, !frameless)
}

// setResizable sets whether the window can be resized.
func (w *linuxWebviewWindow) setResizable(resizable bool) {
	cefSetResizable(w.window, resizable)
}

// setBackgroundColour is a Phase 1 stub. Real implementation in Phase 4.
func (w *linuxWebviewWindow) setBackgroundColour(colour RGBA) {
	_ = colour
}

// setTransparent is a Phase 1 stub. Real implementation in Phase 4.
func (w *linuxWebviewWindow) setTransparent() {}

// setDefaultSize sets the GTK host window's default size. GTK
// applies this when the window is first realised.
func (w *linuxWebviewWindow) setDefaultSize(width, height int) {
	if w.window == nil || width <= 0 || height <= 0 {
		return
	}
	cefSetWindowDefaultSize(w.window, width, height)
}

func (w *linuxWebviewWindow) setAlwaysOnTop(alwaysOnTop bool) {
	cefSetAlwaysOnTop(w.window, alwaysOnTop)
}

// cut / copy / paste / selectAll / undo / redo are Phase 1 stubs that
// proxy to CefFrame::ExecuteJavaScript("document.execCommand(...)"). Real
// implementations wire this through CefFrame in Phase 4.
func (w *linuxWebviewWindow) cut()       { w.execJS("document.execCommand('cut')") }
func (w *linuxWebviewWindow) copyCmd()   { w.execJS("document.execCommand('copy')") }
func (w *linuxWebviewWindow) paste()     { w.execJS("document.execCommand('paste')") }
func (w *linuxWebviewWindow) selectAll() { w.execJS("document.execCommand('selectAll')") }
func (w *linuxWebviewWindow) undo()      { w.execJS("document.execCommand('undo')") }
func (w *linuxWebviewWindow) redo()      { w.execJS("document.execCommand('redo')") }
func (w *linuxWebviewWindow) delete()    { w.execJS("document.execCommand('delete')") }

// flash is a Phase 1 stub; real impl in Phase 4 uses gtk_window_set_urgency_hint.
func (w *linuxWebviewWindow) flash(_ bool) {}

// getScreen is a Phase 1 stub.
func (w *linuxWebviewWindow) getScreen() (*Screen, error) {
	return &Screen{}, nil
}

// size returns the window's current size.
func (w *linuxWebviewWindow) size() (int, int) {
	if w.window == nil {
		return w.lastWidth, w.lastHeight
	}
	cw, ch := cefWidgetWidth(w.window), cefWidgetHeight(w.window)
	if cw > 0 && ch > 0 {
		w.lastWidth, w.lastHeight = cw, ch
	}
	return cw, ch
}

// reload, zoomReset, zoomIn, zoomOut, getZoom, setZoom, zoom, setHTML are
// Phase 1 stubs. Real implementations in Phase 2+ use the CEF API.
func (w *linuxWebviewWindow) reload()      { w.browser.ReloadIgnoreCache() }
func (w *linuxWebviewWindow) zoomReset()   { w.browser.GetHost().SetZoomLevel(0) }
func (w *linuxWebviewWindow) zoomIn()      {
	host := w.browser.GetHost()
	host.SetZoomLevel(host.GetZoomLevel() + 0.5)
}
func (w *linuxWebviewWindow) zoomOut() {
	host := w.browser.GetHost()
	host.SetZoomLevel(host.GetZoomLevel() - 0.5)
}
func (w *linuxWebviewWindow) getZoom() float64 { return w.browser.GetHost().GetZoomLevel() }
func (w *linuxWebviewWindow) setZoom(z float64) { w.browser.GetHost().SetZoomLevel(z) }
// zoom() is already declared as a no-op stub above (Phase 1).
func (w *linuxWebviewWindow) setHTML(html string) {
	if w.browser == nil {
		return
	}
	frame := w.browser.GetMainFrame()
	if frame == nil {
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(html))
	frame.LoadURL("data:text/html;charset=utf-8;base64," + encoded)
}

// isFullscreen / isMaximised / isMinimised / isVisible / isFocused
func (w *linuxWebviewWindow) isFullscreen() bool { return cefIsFullscreen(w.window) }
func (w *linuxWebviewWindow) isMaximised() bool  { return cefIsMaximised(w.window) }
func (w *linuxWebviewWindow) isMinimised() bool  { return cefIsMinimised(w.window) }
func (w *linuxWebviewWindow) isFocused() bool    { return cefIsFocused(w.window) }
func (w *linuxWebviewWindow) isVisible() bool    { return cefIsVisible(w.window) }

// close is a Phase 1 stub. Real implementation in Phase 4 uses
// CefBrowser::CloseBrowser + gtk_window_close.
func (w *linuxWebviewWindow) closeWindow() { cefCloseWindow(w.window) }

// handleKeyEvent / handleNonClientRegionMessage are Phase 1 stubs.
func (w *linuxWebviewWindow) handleKeyEvent(_ string) {}
func (w *linuxWebviewWindow) handleNonClientRegionMessage(_, _ int) {}

// setEnabled / enableDND / disableDND are Phase 1 stubs.
func (w *linuxWebviewWindow) setEnabled(_ bool) {}
func (w *linuxWebviewWindow) enableDND()        {}
func (w *linuxWebviewWindow) disableDND()       {}

// position / setBounds / setPosition / centerOnScreen / centre / center
func (w *linuxWebviewWindow) position() (int, int)                  { return cefGetWindowPosition(w.window) }
func (w *linuxWebviewWindow) relativePosition() (int, int)          { return w.position() }
func (w *linuxWebviewWindow) setBounds(r Rect)                      { w.move(r.X, r.Y); w.setSize(r.Width, r.Height) }
func (w *linuxWebviewWindow) setPosition(x, y int)                 { w.move(x, y) }
func (w *linuxWebviewWindow) centerOnScreen(_ *Screen)             { w.center() }
func (w *linuxWebviewWindow) physicalBounds() Rect                  { return w.bounds() }
func (w *linuxWebviewWindow) setPhysicalBounds(r Rect)             { w.setBounds(r) }
func (w *linuxWebviewWindow) centre()                               { w.center() }
func (w *linuxWebviewWindow) center() {
	mx, my, mw, mh := cefGetCurrentMonitorGeometry(w.window)
	cw, ch := cefGetDefaultSize(w.window)
	cefMoveWindow(w.window, mx+(mw-cw)/2, my+(mh-ch)/2)
}
func (w *linuxWebviewWindow) getCurrentMonitorGeometry() (int, int, int, int) {
	return cefGetCurrentMonitorGeometry(w.window)
}

func (w *linuxWebviewWindow) maximise()   { cefMaximiseWindow(w.window) }
func (w *linuxWebviewWindow) unmaximise() { cefUnmaximiseWindow(w.window) }
func (w *linuxWebviewWindow) minimise()   { cefMinimiseWindow(w.window) }
func (w *linuxWebviewWindow) unminimise()  { cefUnminimiseWindow(w.window) }
func (w *linuxWebviewWindow) unminimise2() { cefUnminimiseWindow(w.window) }
func (w *linuxWebviewWindow) fullscreen()  { cefFullscreenWindow(w.window) }
func (w *linuxWebviewWindow) unfullscreen() {
	cefUnfullscreenWindow(w.window)
	cefUnmaximiseWindow(w.window)
}
func (w *linuxWebviewWindow) move(x, y int) {
	cefMoveWindow(w.window, x, y)
}
func (w *linuxWebviewWindow) present()      { cefPresentWindow(w.window) }
func (w *linuxWebviewWindow) restore()      { cefUnminimiseWindow(w.window); cefUnmaximiseWindow(w.window); cefUnfullscreenWindow(w.window) }