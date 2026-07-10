//go:build linux && cgo && cef && !android && !server

package application

/*
#cgo pkg-config: gtk4
#cgo pkg-config: gtk4-x11
#cgo pkg-config: x11
#cgo pkg-config: gio-unix-2.0

#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <gdk/x11/gdkx.h>
#include <gio/gio.h>
#include <X11/Xlib.h>

// Trivial callback used to satisfy g_application's "activate" signal.
void cef_activate_cb(GApplication *app, gpointer data) {
	(void) app; (void) data;
}

// cef_attach_to_gtk_widget reparents the X11 window `cef_window_xid` (the
// host window created by CEF for its browser view) as a child of the GTK
// widget `parent_widget`. This makes the CEF view render inside the GTK
// container.
//
// GTK4 removed gdk_x11_surface_set_embedder (used in GTK3 to embed foreign
// X11 windows). For Phase 1 we use a plain XReparentWindow; the foreign
// window will receive ConfigureNotify and ResizeRedirect events as if it
// were a managed child. This is enough for static-size windows; Phase 4
// will add resize tracking via XSelectInput + ConfigureNotify handler.
static void cef_attach_to_gtk_widget(unsigned long parent_widget, unsigned long cef_window_xid) {
	GtkWidget *widget = (GtkWidget *)parent_widget;
	Window xid = (Window)cef_window_xid;

	if (!GTK_IS_WIDGET(widget) || xid == 0) {
		return;
	}

	// Realize the widget so it has an X11 window we can reparent under.
	if (!gtk_widget_get_realized(widget)) {
		gtk_widget_realize(widget);
	}

	GtkNative *native = gtk_widget_get_native(widget);
	if (!native) {
		return;
	}

	GdkSurface *surface = gtk_native_get_surface(native);
	if (!surface || !GDK_IS_X11_SURFACE(surface)) {
		return;
	}

	Window parent_xid = gdk_x11_surface_get_xid(surface);
	GdkDisplay *display = gdk_surface_get_display(surface);
	Display *xdisplay = gdk_x11_display_get_xdisplay(display);

	// XReparentWindow moves the CEF window under our GTK surface's X11 window.
	XReparentWindow(xdisplay, xid, parent_xid, 0, 0);

	// Subscribe to ConfigureNotify so we can resize CEF's window when the
	// GTK widget resizes. (Phase 4 will wire this up properly.)
	XSelectInput(xdisplay, xid, StructureNotifyMask);

	// Map the CEF window so it becomes visible.
	XMapWindow(xdisplay, xid);
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
)

// pointer and windowPointer are aliased here so that the CEF backend
// (whose webview_window_linux_cef.go lives behind a `cef` build tag)
// can refer to GTK object handles without depending on linux_cgo.go
// (which has tag `!cef`). The underlying types are identical.
type (
	pointer       unsafe.Pointer
	windowPointer *C.GtkWindow
)

type dragInfo struct {
	XRoot       int
	YRoot       int
	DragTime    uint32
	MouseButton uint
}

// cefInitOnce tracks whether CEF has been initialized in this process.
var (
	cefInitOnce bool
	cefSettings cef.Settings
)

// cefInit initializes the CEF runtime. Idempotent.
//
// Must be called from the GTK main thread BEFORE the GTK main loop starts.
// CEF's message loop is pumped manually via cefDoMessageLoopWork() inside
// the GTK main loop iteration.
//
// Runtime discovery (in order):
//  1. CEF_DIR environment variable
//  2. /usr/lib/cef (Arch Linux cef package)
//  3. ~/.local/share/cef
//
// Returns an error if libcef.so cannot be loaded.
func cefInit() error {
	if cefInitOnce {
		return nil
	}
	cefInitOnce = true

	cefSettings = cef.Settings{
		MultiThreadedMessageLoop: false, // We pump manually from GTK loop.
		ExternalMessagePump:      true,
		NoSandbox:                true, // Required when running as non-root in containers.
		LogSeverity:              99,   // LOGSEVERITY_DISABLE = verbose off.
	}

	// MaybeExitSubprocess runs os.Exit(0) if this process is a CEF helper
	// subprocess (renderer, GPU, etc.).
	cef.MaybeExitSubprocess()

	return cef.Init(cefSettings)
}

// cefShutdown shuts down the CEF runtime.
func cefShutdown() {
	if !cefInitOnce {
		return
	}
	cef.Shutdown()
	cefInitOnce = false
}

// cefDoMessageLoopWork performs one iteration of the CEF message loop.
// Must be called from the GTK main thread.
func cefDoMessageLoopWork() {
	cef.DoMessageLoopWork()
}

// cefAttachToGTKWidget reparents the X11 window backing a CEF browser view
// as a child of the given GTK widget, so the CEF view fills the widget's
// content area.
//
//   - gtkWidget: typically a GtkBox inside the application window.
//   - cefWindowXID: the X11 window ID returned by CEF for the browser view
//     (X11WindowHandle property on the CefBrowser).
func cefAttachToGTKWidget(gtkWidget unsafe.Pointer, cefWindowXID uintptr) {
	if gtkWidget == nil || cefWindowXID == 0 {
		return
	}
	C.cef_attach_to_gtk_widget(C.ulong(uintptr(gtkWidget)), C.ulong(cefWindowXID))
}

// -----------------------------------------------------------------------------
// Application main loop.
// -----------------------------------------------------------------------------

// appRun runs the GTK main loop. Mirrors the GTK4 default's
// implementation: g_application_hold + g_application_run. The
// "activate" signal is connected to a trivial C callback
// (cef_activate_cb) defined in the cgo block above.
//
// CEF's message loop is pumped separately by the browser-process
// thread; CefBrowserHost::CreateBrowser returns once the render
// process is ready and CEF keeps running independently of the GTK
// loop. We don't need to integrate the two loops further for Phase 1.
func appRun(app pointer) error {
	application := (*C.GApplication)(app)
	C.g_application_hold(application)

	// Connect "activate" to the trivial callback. We use
	// g_signal_connect_data (the non-macro variant) so cgo's type
	// checker sees the function pointer correctly.
	activate := C.CString("activate")
	defer C.free(unsafe.Pointer(activate))
	C.g_signal_connect_data(
		C.gpointer(unsafe.Pointer(application)),
		activate,
		(*[0]byte)(unsafe.Pointer(C.cef_activate_cb)),
		nil,
		nil,
		0,
	)

	status := C.g_application_run(application, 0, nil)
	_ = status
	return nil
}

// appNew wraps gtk_application_new with the G_APPLICATION_FLAGS_NONE flag.
func appNew(name string) pointer {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return pointer(C.gtk_application_new(cName, C.G_APPLICATION_FLAGS_NONE))
}

// cefCreateHostWindow creates a new GtkApplicationWindow + GtkBox pair and
// returns them as opaque pointers. The window is NOT shown yet — call
// cefShowWindow for that.
//
// windowId is the wails WebviewWindow ID (for window-map bookkeeping).
func cefCreateHostWindow(application pointer, windowId uint) (window, vbox pointer) {
	window = pointer(C.gtk_application_window_new((*C.GtkApplication)(application)))
	C.g_object_ref_sink(C.gpointer(window))

	vbox = pointer(C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 0))
	C.gtk_window_set_child((*C.GtkWindow)(window), (*C.GtkWidget)(vbox))
	return
}

// cefSetWindowTitle sets the GTK host window's title (which appears in the
// window decoration / taskbar — the CEF content's <title> is independent).
func cefSetWindowTitle(window pointer, title string) {
	if window == nil {
		return
	}
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	C.gtk_window_set_title((*C.GtkWindow)(window), cTitle)
}

func cefShowWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_present((*C.GtkWindow)(window))
}

func cefHideWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_widget_set_visible((*C.GtkWidget)(window), C.gboolean(0))
}

func cefPresentWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_present((*C.GtkWindow)(window))
}

func cefCloseWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_close((*C.GtkWindow)(window))
}

func cefDestroyWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_destroy((*C.GtkWindow)(window))
}

// cefCreateBrowserInWidget creates a CEF browser and attaches its view to
// the given GTK widget via X11 window reparenting.
//
// We use CEF's "child window" mode (ParentWindow set to the GTK widget's
// X11 handle) so CEF creates a child X11 window inside our GTK widget
// and draws into it directly. This is the same approach used by the
// reference CEF GTK sample.
//
// Implementation note: Phase 4.2 fixes the Phase 1 SetAsWindowless stub
// which produced an invisible browser. The new path uses:
//   1. gtk_widget_get_native → gtk_native_get_surface →
//      gdk_x11_surface_get_xid to read the widget's X11 handle.
//   2. Set ParentWindow on the WindowInfo to the widget XID.
//   3. BrowserHostCreateBrowserSync creates a child X11 window under
//      the widget, visible as soon as XMapWindow runs.
func cefCreateBrowserInWidget(gtkWidget unsafe.Pointer, url string) cef.Browser {
	if gtkWidget == nil {
		return nil
	}
	if url == "" {
		url = "about:blank"
	}

	stub := &cefClientStub{}
	rawClient := cef.NewClient(stub)

	// Step 1: realize the widget so it has a GdkSurface.
	C.gtk_widget_realize((*C.GtkWidget)(gtkWidget))

	// Step 2: walk widget -> GtkNative -> GdkSurface.
	native := C.gtk_widget_get_native((*C.GtkWidget)(gtkWidget))
	if native == nil {
		return nil
	}
	surface := C.gtk_native_get_surface(native)
	if surface == nil {
		return nil
	}
	xid := C.gdk_x11_surface_get_xid(surface)
	if xid == 0 {
		return nil
	}

	wi := cef.NewWindowInfo()
	// Tell CEF to create a child X11 window inside our GTK widget.
	// ParentWindow = the host widget's XID. CEF picks the child
	// Window XID itself. CEFWindowHandleT is uint64 on Linux.
	wi.ParentWindow = uint64(xid)
	wi.WindowlessRenderingEnabled = 0
	wi.SharedTextureEnabled = 0
	wi.ExternalBeginFrameEnabled = 0

	settings := cef.NewBrowserSettings()

	fmt.Fprintf(os.Stderr, "wails/cef: BrowserHostCreateBrowserSync url=%q parentXID=%d\n", url, uint64(xid))
	browser := cef.BrowserHostCreateBrowserSync(&wi, rawClient, url, &settings, nil, nil)
	fmt.Fprintf(os.Stderr, "wails/cef: BrowserHostCreateBrowserSync returned browser=%v\n", browser != nil)
	return browser
}