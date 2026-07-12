//go:build linux && cgo && cef && !android && !server

package application

/*
#cgo pkg-config: gtk4
#cgo pkg-config: gtk4-x11
#cgo pkg-config: x11
#cgo pkg-config: gio-unix-2.0

#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <gdk/gdkdisplaymanager.h>
#include <gdk/x11/gdkx.h>
#include <gdk/x11/gdkx11surface.h>
#include <gio/gio.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>

// Trivial callback used to satisfy g_application's "activate" signal.
static void cef_activate_cb(GApplication *app, gpointer data) {
	(void) app; (void) data;
}

// Go owns the callback registry. GLib only needs an integer key to dispatch
// a queued function on the thread that owns the default main context.
extern void dispatchOnMainThreadCallback(unsigned int id);

static gboolean cef_dispatch_on_main_thread_cb(gpointer data) {
	dispatchOnMainThreadCallback(GPOINTER_TO_UINT(data));
	return G_SOURCE_REMOVE;
}

static void cef_dispatch_on_main_thread(unsigned int id) {
	g_idle_add_full(
		G_PRIORITY_DEFAULT,
		cef_dispatch_on_main_thread_cb,
		GUINT_TO_POINTER(id),
		NULL
	);
}

static gboolean cef_is_on_main_thread(void) {
	return g_main_context_is_owner(g_main_context_default());
}

// ── CEF view tracking list ──────────────────────────────────────────
// A simple singly-linked list of (box, xid, last known size) entries.
// On each idle pump tick we walk the list and resize any view whose
// box allocation has changed.  This avoids any dependency on GObject
// property notification timing, which is unreliable during X11 WM
// resize/maximize events.
struct cef_view_entry {
	GtkWidget *box;
	Window     xid;
	int        last_w;
	int        last_h;
	struct cef_view_entry *next;
};

static struct cef_view_entry *cef_view_list = NULL;

// Guards the shared linked list.  All operations happen on the main
// GTK thread (the idle callback runs on it), so we only need the
// guard to keep the C compiler / thread-sanitizer happy.
static GMutex cef_view_mutex;

// cef_view_check_resize checks whether the GtkBox backing a CEF view
// has changed size and, if so, calls XResizeWindow.  Returns 1 if the
// size changed (so the pump can collapse consecutive identical sizes).
static int cef_view_check_resize(struct cef_view_entry *entry) {
	if (!entry || !GTK_IS_WIDGET(entry->box) || entry->xid == 0) {
		return 0;
	}
	int w = gtk_widget_get_width(entry->box);
	int h = gtk_widget_get_height(entry->box);
	if (w <= 0 || h <= 0) {
		return 0;
	}
	if (w == entry->last_w && h == entry->last_h) {
		return 0;
	}
	entry->last_w = w;
	entry->last_h = h;

	GtkNative *native = gtk_widget_get_native(entry->box);
	if (!native) return 0;
	GdkSurface *surface = gtk_native_get_surface(native);
	if (!surface || !GDK_IS_X11_SURFACE(surface)) return 0;
	Display *xdisplay = gdk_x11_display_get_xdisplay(
		gdk_surface_get_display(surface)
	);
	XResizeWindow(xdisplay, entry->xid, (unsigned)w, (unsigned)h);
	XFlush(xdisplay);
	return 1;
}

// cef_view_list_add appends a (box, xid) pair to the tracking list.
static void cef_view_list_add(GtkWidget *box, Window xid) {
	if (!box || !GTK_IS_WIDGET(box) || xid == 0) return;
	g_mutex_lock(&cef_view_mutex);
	struct cef_view_entry *e = g_new0(struct cef_view_entry, 1);
	e->box    = box;
	e->xid    = xid;
	e->last_w = -1;
	e->last_h = -1;
	e->next   = cef_view_list;
	cef_view_list = e;
	g_mutex_unlock(&cef_view_mutex);
}

// cef_view_list_remove removes the entry matching XID from the list.
static void cef_view_list_remove(Window xid) {
	if (xid == 0) return;
	g_mutex_lock(&cef_view_mutex);
	struct cef_view_entry **pp = &cef_view_list;
	while (*pp) {
		if ((*pp)->xid == xid) {
			struct cef_view_entry *tmp = *pp;
			*pp = tmp->next;
			g_free(tmp);
			break;
		}
		pp = &(*pp)->next;
	}
	g_mutex_unlock(&cef_view_mutex);
}

// cef_view_list_walk iterates every tracked view and resizes it if
// the backing GtkBox has changed since the last check.
// Returns the number of views still alive (total entries).
static int cef_view_list_walk(void) {
	int alive = 0;
	g_mutex_lock(&cef_view_mutex);
	struct cef_view_entry *cur = cef_view_list;
	while (cur) {
		if (GTK_IS_WIDGET(cur->box)) {
			cef_view_check_resize(cur);
			alive++;
		}
		cur = cur->next;
	}
	g_mutex_unlock(&cef_view_mutex);
	return alive;
}

// cef_pump_message_loop_cb is installed via g_idle_add_full at
// G_PRIORITY_DEFAULT_IDLE so it runs every GTK idle cycle. CEF uses
// ExternalMessagePump + a manual pump (cef.DoMessageLoopWork) when
// MultiThreadedMessageLoop is false. Without this pump, the GTK
// main loop starves CEF — Chromium IPC callbacks never fire and the
// host window never actually paints.
//
// We also walk the CEF view tracking list to catch layout changes
// caused by maximize/tile/unmaximize that GObject property
// notification signals are too unreliable to deliver.
extern void doMessageLoopWorkCallback(void);

static gboolean cef_pump_message_loop_cb(gpointer data) {
	(void) data;
	doMessageLoopWorkCallback();
	cef_view_list_walk();
	return G_SOURCE_CONTINUE;
}

static guint cef_install_message_pump(void) {
	return g_idle_add_full(
		G_PRIORITY_DEFAULT_IDLE,
		cef_pump_message_loop_cb,
		NULL,
		NULL
	);
}

static void cef_connect_activate(GApplication *app) {
	g_signal_connect_data(
		app,
		"activate",
		G_CALLBACK(cef_activate_cb),
		NULL,
		NULL,
		0
	);
}

// cef_resize_cef_view resizes the CEF view's X11 window to fill the
// GtkBox's current allocated size. Called immediately after
// reparenting and also from the idle pump when the box size changes.
static void cef_resize_cef_view(GtkWidget *widget, Window xid) {
	if (!GTK_IS_WIDGET(widget) || xid == 0) {
		return;
	}
	GtkNative *native = gtk_widget_get_native(widget);
	if (!native) {
		return;
	}
	GdkSurface *surface = gtk_native_get_surface(native);
	if (!surface || !GDK_IS_X11_SURFACE(surface)) {
		return;
	}
	GdkDisplay *display = gdk_surface_get_display(surface);
	Display *xdisplay = gdk_x11_display_get_xdisplay(display);

	int w = gtk_widget_get_width(widget);
	int h = gtk_widget_get_height(widget);
	if (w <= 0 || h <= 0) {
		return;
	}
	XResizeWindow(xdisplay, xid, (unsigned)w, (unsigned)h);
	XFlush(xdisplay);
}

// cef_attach_to_gtk_widget reparents the X11 window `cef_window_xid`
// (the host window created by CEF for its browser view) as a child of
// the GTK widget `parent_widget`, then adds the (box, xid) pair to
// the tracking list so the idle message pump keeps the CEF view size
// in sync with the GtkBox allocation.
//
// We do NOT connect GObject signal handlers for resize because
// GtkWindow::notify::width/height fires before the child widget is
// re-allocated during maximize events, and GtkBox does not fire
// notify signals at all for WM-initiated size changes.  Instead the
// idle pump (cef_view_list_walk) polls the GtkBox size on every
// idle tick and issues XResizeWindow only when the size changes.
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

	// XReparentWindow moves the CEF window under our GTK surface's
	// X11 window. Place it at (0,0); the idle pump will size it.
	XReparentWindow(xdisplay, xid, parent_xid, 0, 0);

	// Subscribe to ConfigureNotify on the CEF window itself in case
	// the user resizes via CEF DevTools.
	XSelectInput(xdisplay, xid, StructureNotifyMask);

	// Map the CEF window so it becomes visible.
	XMapWindow(xdisplay, xid);

	// Register this (box, xid) pair in the tracking list.  The idle
	// pump picks it up on the next tick and issues the initial resize.
	cef_view_list_add(widget, xid);
}

// ── X11 helpers for window management ─────────────────────────────
// These are used in place of gtk_window_move (removed in GTK4) and for
// window state operations that GTK4 doesn't expose directly.

static void window_move_x11(GtkWindow *window, int x, int y) {
	GtkNative *native = gtk_widget_get_native(GTK_WIDGET(window));
	if (native == NULL) return;

	GdkSurface *surface = gtk_native_get_surface(native);
	if (surface == NULL) return;

	Display *xdisplay = gdk_x11_display_get_xdisplay(gdk_surface_get_display(surface));
	Window xwindow = gdk_x11_surface_get_xid(GDK_X11_SURFACE(surface));
	XMoveWindow(xdisplay, xwindow, x, y);
	XFlush(xdisplay);
}

static void window_get_position_x11(GtkWindow *window, int *x, int *y) {
	*x = 0; *y = 0;
	GtkNative *native = gtk_widget_get_native(GTK_WIDGET(window));
	if (native == NULL) return;

	GdkSurface *surface = gtk_native_get_surface(native);
	if (surface == NULL) return;

	Display *xdisplay = gdk_x11_display_get_xdisplay(gdk_surface_get_display(surface));
	Window xwindow = gdk_x11_surface_get_xid(GDK_X11_SURFACE(surface));

	Window child, root;
	root = DefaultRootWindow(xdisplay);
	int abs_x, abs_y;
	if (XTranslateCoordinates(xdisplay, xwindow, root, 0, 0, &abs_x, &abs_y, &child)) {
		*x = abs_x;
		*y = abs_y;
	}
}

static void window_set_always_on_top_x11(GtkWindow *window, int always_on_top) {
	GtkNative *native = gtk_widget_get_native(GTK_WIDGET(window));
	if (native == NULL) return;

	GdkSurface *surface = gtk_native_get_surface(native);
	if (surface == NULL) return;

	Display *xdisplay = gdk_x11_display_get_xdisplay(gdk_surface_get_display(surface));
	Window xwindow = gdk_x11_surface_get_xid(GDK_X11_SURFACE(surface));

	Atom wm_state = XInternAtom(xdisplay, "_NET_WM_STATE", False);
	Atom above = XInternAtom(xdisplay, "_NET_WM_STATE_ABOVE", False);
	Window root = DefaultRootWindow(xdisplay);
	XEvent event;
	memset(&event, 0, sizeof(event));
	event.type = ClientMessage;
	event.xclient.window = xwindow;
	event.xclient.message_type = wm_state;
	event.xclient.format = 32;
	event.xclient.data.l[0] = always_on_top ? 1 : 0; // _NET_WM_STATE_ADD or _NET_WM_STATE_REMOVE
	event.xclient.data.l[1] = (long)above;
	event.xclient.data.l[2] = 0;
	event.xclient.data.l[3] = 0;
	event.xclient.data.l[4] = 0;

	XSendEvent(xdisplay, root, False, SubstructureRedirectMask | SubstructureNotifyMask, &event);
	XFlush(xdisplay);
}

static void window_set_max_size_x11(GtkWindow *window, int maxWidth, int maxHeight) {
	if (maxWidth <= 0 && maxHeight <= 0) return;

	GtkNative *native = gtk_widget_get_native(GTK_WIDGET(window));
	if (native == NULL) return;

	GdkSurface *surface = gtk_native_get_surface(native);
	if (surface == NULL) return;

	Display *xdisplay = gdk_x11_display_get_xdisplay(gdk_surface_get_display(surface));
	Window xwindow = gdk_x11_surface_get_xid(GDK_X11_SURFACE(surface));

	XSizeHints hints;
	memset(&hints, 0, sizeof(hints));
	hints.flags = PMaxSize;
	if (maxWidth > 0) hints.max_width = maxWidth;
	if (maxHeight > 0) hints.max_height = maxHeight;
	XSetWMNormalHints(xdisplay, xwindow, &hints);
	XFlush(xdisplay);
}

static gboolean window_is_minimised(GtkWindow *window) {
	GtkNative *native = gtk_widget_get_native(GTK_WIDGET(window));
	if (native == NULL) return FALSE;

	GdkSurface *surface = gtk_native_get_surface(native);
	if (surface == NULL) return FALSE;

	GdkToplevelState state = gdk_toplevel_get_state(GDK_TOPLEVEL(surface));
	return (state & GDK_TOPLEVEL_STATE_MINIMIZED) != 0;
}

// processWindowEvent is the same forwarder used by the WebKit backend's
// cgo block; we redeclare it here because the WebKit block is excluded
// from the CEF build tag. The implementation lives in application_linux_cef.go
// (or a CEF-only sibling) and writes to windowEvents.
extern void processWindowEvent(unsigned int windowID, unsigned int eventID);

// cef_focus_enter_cb / cef_focus_leave_cb forward GTK4 focus events to
// the same channel the WebKit backend uses. data is the wails
// WebviewWindow id, captured by g_signal_connect_data above.
//
// The event IDs are the events.Linux.WindowFocusIn / WindowFocusOut
// constants (1059 / 1060). Hardcoded here so the cgo preamble stays
// free of dependency on the events package (Go constants are not
// visible from C).
static void cef_focus_enter_cb(GtkEventController *controller, gpointer data) {
	(void) controller;
	processWindowEvent(GPOINTER_TO_UINT(data), 1059);
}

static void cef_focus_leave_cb(GtkEventController *controller, gpointer data) {
	(void) controller;
	processWindowEvent(GPOINTER_TO_UINT(data), 1060);
}

// cef_install_focus_controller attaches a GtkEventControllerFocus to
// the given GtkWindow and wires its enter/leave signals to
// cef_focus_enter_cb / cef_focus_leave_cb, carrying windowId as the
// user-data pointer. Keeps the gpointer cast purely in C so cgo's
// stricter uintptr_t → gpointer rules don't bite us.
static void cef_install_focus_controller(GtkWidget *window, gpointer window_id) {
	GtkEventController *controller = gtk_event_controller_focus_new();
	gtk_widget_add_controller(window, controller);
	g_signal_connect_data(controller, "enter",
		G_CALLBACK(cef_focus_enter_cb), window_id, NULL, 0);
	g_signal_connect_data(controller, "leave",
		G_CALLBACK(cef_focus_leave_cb), window_id, NULL, 0);
}

// ── Window signal handlers ──────────────────────────────────────────

// cef_close_request_cb is called when the user clicks the close button
// or the WM sends a close request. It emits WindowDeleteEvent and
// returns GDK_EVENT_PROPAGATE so GTK continues with the default close.
static gboolean cef_close_request_cb(GtkWidget *widget, gpointer data) {
	(void) widget;
	processWindowEvent(GPOINTER_TO_UINT(data), 1056);
	return GDK_EVENT_PROPAGATE;
}

// cef_window_state_notify_cb is called when the maximized or
// fullscreened property changes. Emits WindowDidResize so the Wails
// event system can react to the state transition.
static void cef_window_state_notify_cb(GObject *obj, GParamSpec *pspec, gpointer data) {
	(void) obj; (void) pspec;
	processWindowEvent(GPOINTER_TO_UINT(data), 1078);
}

// cef_install_window_signal_handlers connects close-request and
// window-state change signals on the given GtkWindow so that window
// lifecycle events propagate to the Wails event bus.
static void cef_install_window_signal_handlers(GtkWindow *window, gpointer window_id) {
	g_signal_connect_data(window, "close-request",
		G_CALLBACK(cef_close_request_cb), window_id, NULL, 0);
	g_signal_connect_data(window, "notify::maximized",
		G_CALLBACK(cef_window_state_notify_cb), window_id, NULL, 0);
	g_signal_connect_data(window, "notify::fullscreened",
		G_CALLBACK(cef_window_state_notify_cb), window_id, NULL, 0);
}


*/
import "C"

import (
	"os"
	"path/filepath"
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

	// Debug: dump GDK-related env vars BEFORE CEF init. GTK will
	// auto-open the default display when gtk_application_new is
	// called; if the user wants X11, they need to set GDK_BACKEND=x11
	// BEFORE the first GTK call. CEF's subprocess forking preserves
	// the env, but GTK only reads the variable at first init.
	gdkBackend := os.Getenv("GDK_BACKEND")
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	xDisplay := os.Getenv("DISPLAY")
	xdgSession := os.Getenv("XDG_SESSION_TYPE")
	debugLog("[cefInit] env GDK_BACKEND=%q WAYLAND_DISPLAY=%q DISPLAY=%q XDG_SESSION_TYPE=%q", gdkBackend, waylandDisplay, xDisplay, xdgSession)

	// If WAYLAND_DISPLAY is set in the environment but the user
	// requested X11 via GDK_BACKEND, drop the Wayland hint so GTK
	// actually uses X11. Modern GTK4 on Wayland compositors prefers
	// the native Wayland backend even when GDK_BACKEND=x11 is set,
	// unless WAYLAND_DISPLAY is unset.
	if gdkBackend == "x11" && waylandDisplay != "" {
		debugLog("[cefInit] unsetting WAYLAND_DISPLAY to honor GDK_BACKEND=x11")
		_ = os.Unsetenv("WAYLAND_DISPLAY")
	}
	// Belt-and-braces: restrict GDK to a single backend via the
	// runtime API. Must happen before any GTK display is opened.
	//
	// CEF only supports Ozone/X11 reliably.  Force GDK to X11
	// regardless of session type; on Wayland the compositor provides
	// XWayland (DISPLAY=:1).
	C.gdk_set_allowed_backends(C.CString("x11"))

	C.gtk_init()

	// Sanity-check that we ended up on X11.
	defaultDisplay := C.gdk_display_get_default()
	if defaultDisplay != nil {
		debugLog("[cefInit] post-init default display backend=%s", C.GoString(C.gdk_display_get_name(defaultDisplay)))
	} else {
		debugLog("[cefInit] post-init no default display")
	}

	// Ensure required CEF data files are in the right places before
	// CEF tries to load them (v8_context_snapshot.bin, icudtl.dat).
	cefEnsureFiles()

	resDir := cefResourcesDir()
	locDir := ""
	if resDir != "" {
		locDir = filepath.Join(resDir, "locales")
	}
	multiProcess, helperPath := cefMultiProcessConfig()
	cefSettings = cef.Settings{
		MultiThreadedMessageLoop: false, // We pump manually from GTK loop.
		ExternalMessagePump:      true,
		NoSandbox:                true, // Required when running as non-root in containers.
		LogSeverity:              0,    // LOGSEVERITY_VERBOSE
		LogFile:                  "/tmp/wails-cef.log",
		ResourcesDirPath:         resDir,
		LocalesDirPath:           locDir,
	}
	if multiProcess {
		cefSettings.BrowserSubprocessPath = helperPath
		debugLog("[cefInit] multi-process enabled with helper %s", helperPath)
	} else if os.Getenv("WAILS_CEF_MULTIPROCESS") != "" {
		debugLog("[cefInit] multi-process requested but wails-cef-helper is unavailable; using single-process")
	}

	// Build the CefApp that injects the Chromium command-line switches
	// we need. cef.CommandLineGetGlobal() is read-only and the CEF
	// binding panics on it, so the supported path is to install a
	// CefApp whose OnBeforeCommandLineProcessing appends the switches
	// before CEF parses argv.
	cefApp := &cefWailsApp{}
	// ExecuteSubprocess runs cef_execute_process to check if this process is a
	// CEF helper subprocess (renderer, GPU, utility, etc.). Unlike the nil-App
	// variant (MaybeExitSubprocess), we pass our cefWailsApp so subprocesses
	// also receive OnBeforeCommandLineProcessing to apply our flags.
	if executed, code, err := cef.ExecuteSubprocessWithApp(cefApp); err != nil {
		debugLog("[cefInit] ExecuteSubprocessWithApp error: %v", err)
	} else if executed {
		debugLog("[cefInit] exiting as CEF subprocess (code=%d)", code)
		os.Exit(code)
	}
	if err := cef.InitWithApp(cefSettings, cefApp); err != nil {
		return err
	}
	cefInitOnce = true
	return nil
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

func cefDispatchOnMainThread(id uint) {
	C.cef_dispatch_on_main_thread(C.uint(id))
}

func cefIsOnMainThread() bool {
	return C.cef_is_on_main_thread() != 0
}

//export dispatchOnMainThreadCallback
func dispatchOnMainThreadCallback(callbackID C.uint) {
	executeOnMainThread(uint(callbackID))
}

//export doMessageLoopWorkCallback
func doMessageLoopWorkCallback() {
	cefDoMessageLoopWork()
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
// "activate" signal is connected to a trivial callback
// (cef_activate_cb) defined in the cgo block above.
//
// CEF's message loop is pumped separately by the browser-process
// thread; CefBrowserHost::CreateBrowser returns once the render
// process is ready and CEF keeps running independently of the GTK
// loop. We don't need to integrate the two loops further for Phase 1.
func appRun(app pointer) error {
	application := (*C.GApplication)(app)
	C.g_application_hold(application)

	C.cef_connect_activate(application)

	// Install the CEF message pump on the GTK idle queue BEFORE
	// entering g_application_run. CEF runs with ExternalMessagePump
	// and MultiThreadedMessageLoop=false, so we have to call
	// cef.DoMessageLoopWork ourselves or Chromium IPC stalls and the
	// window never paints.
	C.cef_install_message_pump()

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
// Focus events (WindowFocusIn/WindowFocusOut) are wired here via a
// GtkEventControllerFocus installed in C (cef_install_focus_controller
// below) — moving the controller setup into the C preamble keeps the
// gpointer casts in C where they belong and avoids cgo type issues
// around C.uintptr_t → C.gpointer.
func cefCreateHostWindow(application pointer, windowId uint) (window, vbox pointer) {
	window = pointer(C.gtk_application_window_new((*C.GtkApplication)(application)))
	C.g_object_ref_sink(C.gpointer(window))

	vbox = pointer(C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 0))
	C.gtk_window_set_child((*C.GtkWindow)(window), (*C.GtkWidget)(vbox))

	// Wire focus-in / focus-out via the C helper. CEF embeds its
	// view as an X11 child of this window; GTK's window-level focus
	// change is the only signal we have for "user clicked the CEF
	// window". The WebKit backend uses an identical controller (see
	// linux_cgo.go: handleFocusEnter / handleFocusLeave). The
	// uintptr_t → gpointer cast happens inside the C helper so we
	// don't have to fight cgo's stricter conversion rules here.
	// windowId is an opaque C-side identifier (not a Go pointer), so
	// the unsafe.Pointer intermediate is safe and intentional.
	C.cef_install_focus_controller((*C.GtkWidget)(window), C.gpointer(unsafe.Pointer(uintptr(windowId))))

	// Wire close-request and window-state change signals so
	// WindowDeleteEvent / WindowDidResize propagate to Wails.
	C.cef_install_window_signal_handlers((*C.GtkWindow)(window), C.gpointer(unsafe.Pointer(uintptr(windowId))))

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

// cefSetWindowSize resizes the GTK host window. GTK4 doesn't expose
// gtk_window_resize; instead we set both the default size and the
// size request, then explicitly allocate the new size.
func cefSetWindowSize(window pointer, width, height int) {
	if window == nil || width <= 0 || height <= 0 {
		return
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(window), C.int(width), C.int(height))
	C.gtk_widget_set_size_request((*C.GtkWidget)(window), C.int(width), C.int(height))
}

// cefSetWindowDefaultSize sets the GTK host window's default size.
// GTK uses this when the window is first shown.
func cefSetWindowDefaultSize(window pointer, width, height int) {
	if window == nil || width <= 0 || height <= 0 {
		return
	}
	C.gtk_window_set_default_size((*C.GtkWindow)(window), C.int(width), C.int(height))
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

// ── Window state operations ───────────────────────────────────────

func cefMaximiseWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_maximize((*C.GtkWindow)(window))
}

func cefUnmaximiseWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_unmaximize((*C.GtkWindow)(window))
}

func cefMinimiseWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_minimize((*C.GtkWindow)(window))
}

func cefUnminimiseWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_unminimize((*C.GtkWindow)(window))
}

func cefFullscreenWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_fullscreen((*C.GtkWindow)(window))
}

func cefUnfullscreenWindow(window pointer) {
	if window == nil {
		return
	}
	C.gtk_window_unfullscreen((*C.GtkWindow)(window))
}

func cefIsFullscreen(window pointer) bool {
	if window == nil {
		return false
	}
	return C.gtk_window_is_fullscreen((*C.GtkWindow)(window)) != 0
}

func cefIsMaximised(window pointer) bool {
	if window == nil {
		return false
	}
	return C.gtk_window_is_maximized((*C.GtkWindow)(window)) != 0 && !cefIsFullscreen(window)
}

func cefIsMinimised(window pointer) bool {
	if window == nil {
		return false
	}
	return C.window_is_minimised((*C.GtkWindow)(window)) != 0
}

func cefIsFocused(window pointer) bool {
	if window == nil {
		return false
	}
	return C.gtk_window_is_active((*C.GtkWindow)(window)) != 0
}

func cefIsVisible(window pointer) bool {
	if window == nil {
		return false
	}
	return C.gtk_widget_is_visible((*C.GtkWidget)(window)) != 0
}

func cefMoveWindow(window pointer, x, y int) {
	if window == nil {
		return
	}
	// Wayland compositors reject explicit window placement; calling
	C.window_move_x11((*C.GtkWindow)(window), C.int(x), C.int(y))
}

func cefGetWindowPosition(window pointer) (int, int) {
	if window == nil {
		return 0, 0
	}
	var x, y C.int
	C.window_get_position_x11((*C.GtkWindow)(window), &x, &y)
	return int(x), int(y)
}

func cefSetResizable(window pointer, resizable bool) {
	if window == nil {
		return
	}
	b := C.gboolean(0)
	if resizable {
		b = C.gboolean(1)
	}
	C.gtk_window_set_resizable((*C.GtkWindow)(window), b)
}

func cefSetDecorated(window pointer, decorated bool) {
	if window == nil {
		return
	}
	b := C.gboolean(0)
	if decorated {
		b = C.gboolean(1)
	}
	C.gtk_window_set_decorated((*C.GtkWindow)(window), b)
}

func cefSetAlwaysOnTop(window pointer, alwaysOnTop bool) {
	if window == nil {
		return
	}
	v := C.int(0)
	if alwaysOnTop {
		v = C.int(1)
	}
	C.window_set_always_on_top_x11((*C.GtkWindow)(window), v)
}

func cefSetSizeRequest(window pointer, width, height int) {
	if window == nil {
		return
	}
	if width > 0 && height > 0 {
		C.gtk_widget_set_size_request((*C.GtkWidget)(window), C.int(width), C.int(height))
	}
}

func cefGetDefaultSize(window pointer) (int, int) {
	if window == nil {
		return 0, 0
	}
	var w, h C.int
	C.gtk_window_get_default_size((*C.GtkWindow)(window), &w, &h)
	if w <= 0 || h <= 0 {
		w = C.int(C.gtk_widget_get_width((*C.GtkWidget)(window)))
		h = C.int(C.gtk_widget_get_height((*C.GtkWidget)(window)))
	}
	return int(w), int(h)
}

func cefSetMaxSize(window pointer, maxWidth, maxHeight int) {
	if window == nil {
		return
	}
	C.window_set_max_size_x11((*C.GtkWindow)(window), C.int(maxWidth), C.int(maxHeight))
}

// ── Monitor query ─────────────────────────────────────────────────

func cefGetCurrentMonitorGeometry(window pointer) (int, int, int, int) {
	if window == nil {
		return 0, 0, 1920, 1080
	}
	surface := C.gtk_native_get_surface(C.gtk_widget_get_native((*C.GtkWidget)(window)))
	if surface == nil {
		return 0, 0, 1920, 1080
	}
	monitor := C.gdk_display_get_monitor_at_surface(C.gdk_surface_get_display(surface), surface)
	if monitor == nil {
		return 0, 0, 1920, 1080
	}
	var geo C.GdkRectangle
	C.gdk_monitor_get_geometry(monitor, &geo)
	return int(geo.x), int(geo.y), int(geo.width), int(geo.height)
}

func cefWidgetWidth(window pointer) int {
	if window == nil {
		return 0
	}
	return int(C.gtk_widget_get_width((*C.GtkWidget)(window)))
}

func cefWidgetHeight(window pointer) int {
	if window == nil {
		return 0
	}
	return int(C.gtk_widget_get_height((*C.GtkWidget)(window)))
}

// cefCreateBrowserDetached creates a CEF browser that lives in its
// own top-level Wayland window. Used on Wayland sessions where we
// can't embed into the GTK4 host (the Wayland protocol has no
// foreign-window equivalent of XReparentWindow). The GTK4 window
// remains as a placeholder host — apps should not assume its size
// reflects the CEF view. Decision C15.
//
// Phase 5 will replace this with an xdg-foreign import path that
// asks the compositor to embed CEF's Wayland wl_surface into our
// GTK4 surface via the gtk_shell1 protocol.
func cefCreateBrowserDetached(gtkWindow unsafe.Pointer, url string, width, height int, w *linuxWebviewWindow) cef.Browser {
	if url == "" {
		url = "about:blank"
	}

	stub := &cefClientStub{w: w}
	rawClient := cef.NewClient(stub)

	// Ensure the GTK window is realized — useful for tests / dev that
	// inspect the host. CEF doesn't need it on Wayland but realizing
	// it now gives the user a visible GTK4 window they can interact
	// with even before CEF is up.
	if !bool(C.gtk_widget_get_realized((*C.GtkWidget)(gtkWindow)) != 0) {
		C.gtk_widget_realize((*C.GtkWidget)(gtkWindow))
	}

	wi := cef.NewWindowInfo()
	wi.WindowlessRenderingEnabled = 0
	wi.SharedTextureEnabled = 0
	wi.ExternalBeginFrameEnabled = 0
	// On Wayland the Chrome runtime is required (the Alloy runtime
	// doesn't have a Wayland surface implementation). The runtime
	// style is selected globally by the cefWailsApp command-line
	// processing, so we don't set it here.
	if width <= 0 {
		width = 800
	}
	if height <= 0 {
		height = 600
	}
	wi.Bounds.X = 0
	wi.Bounds.Y = 0
	wi.Bounds.Width = int32(width)
	wi.Bounds.Height = int32(height)

	settings := cef.NewBrowserSettings()
	debugLog("[cefCreateBrowserDetached] url=%q", url)
	browser := cef.BrowserHostCreateBrowserSync(&wi, rawClient, url, &settings, nil, nil)
	if browser == nil {
		debugLog("[cefCreateBrowserDetached] returned browser=false")
		return nil
	}
	debugLog("[cefCreateBrowserDetached] returned browser=true (CEF owns its own Wayland window)")

	// Register the browser in the global browser→window map so the
	// V8 extension handler can find the owning window when JS calls
	// wails_cefResolveDrop or wails_invokeAsync.
	if w != nil {
		registerCefBrowser(browser.GetIdentifier(), w)
	}
	return browser
}

// cefCreateBrowserInWidget creates a CEF browser, then reparents its
// X11 view into the GtkBox so the browser fills the box's content
// area.
//
// We DON'T pass WindowInfo.ParentWindow to CEF. The reason is a
// MatchError that Chromium raises when it tries to create a child X11
// window with a visual that doesn't match the GTK host window's
// visual. CEF (under --ozone-platform=x11) uses the X display's
// default visual (typically 24-bit TrueColor), but GTK4 on a KDE
// Wayland session picks one of the ARGB32 visuals advertised by the
// compositor via _NET_VISIBLE. The two don't share a visual, so
// XCreateWindow fails with "Match" and the browser never paints.
//
// Instead we let CEF create a top-level X11 window (no ParentWindow),
// then XReparentWindow it into the GtkBox ourselves. The
// cef_attach_to_gtk_widget helper walks widget → GtkNative →
// GdkSurface → XID, so it correctly identifies the GtkWindow as the
// X11 parent of the CEF view even when we hand it a GtkBox (which
// has no native surface of its own).
func cefCreateBrowserInWidget(gtkWindow unsafe.Pointer, gtkBox unsafe.Pointer, url string, width, height int, w *linuxWebviewWindow) cef.Browser {
	if gtkWindow == nil {
		debugLog("[cefCreateBrowserInWidget] gtkWindow is nil")
		return nil
	}
	if url == "" {
		url = "about:blank"
	}

	stub := &cefClientStub{w: w}
	rawClient := cef.NewClient(stub)

	// Realize the GtkWindow so it has a GdkSurface with a valid X11
	// handle. Child widgets (GtkBox) don't have their own native
	// surfaces in GTK4 — only the top-level GtkWindow does.
	if !bool(C.gtk_widget_get_realized((*C.GtkWidget)(gtkWindow)) != 0) {
		C.gtk_widget_realize((*C.GtkWidget)(gtkWindow))
	}

	// Walk GtkWindow -> GtkNative -> GdkSurface -> X11 handle.
	native := C.gtk_widget_get_native((*C.GtkWidget)(gtkWindow))
	if native == nil {
		debugLog("[cefCreateBrowserInWidget] gtk_widget_get_native returned NULL")
		return nil
	}
	surface := C.gtk_native_get_surface(native)
	if surface == nil {
		debugLog("[cefCreateBrowserInWidget] gtk_native_get_surface returned NULL")
		return nil
	}
	parentXID := C.gdk_x11_surface_get_xid(surface)
	if parentXID == 0 {
		debugLog("[cefCreateBrowserInWidget] gdk_x11_surface_get_xid returned 0")
		return nil
	}

	wi := cef.NewWindowInfo()
	// No ParentWindow: let CEF create a top-level X11 window. We
	// reparent it into the GtkBox below.
	wi.WindowlessRenderingEnabled = 0
	wi.SharedTextureEnabled = 0
	wi.ExternalBeginFrameEnabled = 0
	// Force the legacy "Alloy" runtime. CEF 147 defaults to the
	// Chrome runtime which doesn't honour ParentWindow the same way.
	wi.RuntimeStyle = cef.RuntimeStyleAlloy
	// Bounds: initial size for the CEF view. After reparenting into
	// the GtkBox, the deferred idle callback resizes it to match the
	// actual box allocation.
	if width <= 0 {
		width = 800
	}
	if height <= 0 {
		height = 600
	}
	wi.Bounds.X = 0
	wi.Bounds.Y = 0
	wi.Bounds.Width = int32(width)
	wi.Bounds.Height = int32(height)

	settings := cef.NewBrowserSettings()

	debugLog("[cefCreateBrowserInWidget] url=%q gtkWindowXID=%d", url, uint64(parentXID))
	browser := cef.BrowserHostCreateBrowserSync(&wi, rawClient, url, &settings, nil, nil)
	debugLog("[cefCreateBrowserInWidget] returned browser=%v", browser != nil)
	if browser == nil {
		return nil
	}

	// Register the browser in the global browser→window map so the
	// V8 extension handler can find the owning window when JS calls
	// wails_cefResolveDrop during a drop event.
	if w != nil {
		registerCefBrowser(browser.GetIdentifier(), w)
	}

	// Reparent CEF's view window into the GtkBox. cef_attach_to_gtk_widget
	// walks widget -> GtkNative -> surface -> XID, so even when given a
	// GtkBox (no native surface) it correctly resolves the parent XID to
	// the enclosing GtkWindow.
	host := browser.GetHost()
	if host == nil {
		return browser
	}
	view := host.GetWindowHandle()
	debugLog("[cefCreateBrowserInWidget] browser view XID=%d", uint64(view))
	if view == 0 {
		return browser
	}
	target := gtkBox
	if target == nil {
		// Fallback to the GtkWindow if no box was supplied (e.g. the
		// call site hasn't laid out a vbox yet).
		target = unsafe.Pointer(gtkWindow)
	}
	cefAttachToGTKWidget(target, view)
	return browser
}

// -----------------------------------------------------------------------------
// OSR (off-screen rendering) Go-side callbacks
// -----------------------------------------------------------------------------
//
// Called from the C preamble on the main thread (via g_idle_add_full).
// Do NOT call CefBrowserHost / Browser methods that require the CEF
// UI thread directly — instead bounce through InvokeAsync so they
// run on the CEF UI thread (which is the main thread in single-process
// mode but the contract is the same either way).
