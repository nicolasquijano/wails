//go:build linux && cgo && cef && !android && !server

package application

/*
#cgo pkg-config: gtk4
#cgo pkg-config: gtk4-x11
#cgo pkg-config: x11

#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <gdk/x11/gdkx.h>
#include <X11/Xlib.h>

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
// Host GTK window helpers used by webview_window_linux_cef.go.
// These create and manipulate the GtkApplicationWindow that hosts the CEF
// browser view. In Phase 1 they mirror the GTK4 default backend.
// -----------------------------------------------------------------------------

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
// the given GTK widget.
//
// Implementation note: this is a Phase 1 placeholder that creates the CEF
// browser but does NOT yet wire up the asset server scheme handler (Phase 2)
// nor the JS↔Go IPC shim (Phase 3). For Phase 1, browsers load URLs via
// http(s) directly.
func cefCreateBrowserInWidget(gtkWidget unsafe.Pointer, url string) cef.Browser {
	if gtkWidget == nil {
		return nil
	}
	if url == "" {
		url = "about:blank"
	}

	// Phase 2 TODO: implement a CefClient with a CefResourceRequestHandler
	// that bridges wails:// and http://wails.localhost to the asset server.
	// Phase 1 uses a stub client whose handlers are all nil (CEF defaults).
	stub := &cefClientStub{}
	rawClient := cef.NewClient(stub)

	wi := cef.NewWindowInfo()
	// Tell CEF to render into our existing X11 window. The host widget's
	// XID will be discovered later when CEF calls back with the new window.
	// For Phase 1 we use SetAsWindowless; the GTK integration reparent logic
	// above is wired up separately.
	cef.SetAsWindowless(&wi, 0, false)

	settings := cef.NewBrowserSettings()

	return cef.BrowserHostCreateBrowserSync(&wi, rawClient, url, &settings, nil, nil)
}