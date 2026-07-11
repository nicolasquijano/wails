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
*/
import "C"

import (
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
	// Belt-and-braces: also restrict GDK to X11 via the runtime API.
	// This must happen before any GTK display is opened.
	C.gdk_set_allowed_backends(C.CString("x11"))

	// Proactively initialise GTK before CEF does. CEF's library has
	// GTK symbols inside it (it uses GTK for file dialogs etc.) and
	// will likely call gtk_init() during cef_initialize. If we let
	// CEF drive the first GTK init, it happens before we've had a
	// chance to enforce X11 and we end up on a Wayland display.
	//
	// Doing it ourselves first makes GTK honour GDK_BACKEND=x11 and
	// opens an X11 default display that CEF can reuse via
	// gdk_display_get_default().
	C.gtk_init()

	// Sanity-check that we ended up on X11.
	defaultDisplay := C.gdk_display_get_default()
	if defaultDisplay != nil {
		debugLog("[cefInit] post-init default display backend=%s", C.GoString(C.gdk_display_get_name(defaultDisplay)))
	} else {
		debugLog("[cefInit] post-init no default display")
	}

	cefSettings = cef.Settings{
		MultiThreadedMessageLoop: false, // We pump manually from GTK loop.
		ExternalMessagePump:      true,
		NoSandbox:                true, // Required when running as non-root in containers.
		LogSeverity:              0,    // LOGSEVERITY_VERBOSE
		LogFile:                  "/tmp/wails-cef.log",
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
func cefCreateBrowserInWidget(gtkWindow unsafe.Pointer, gtkBox unsafe.Pointer, url string, width, height int) cef.Browser {
	if gtkWindow == nil {
		debugLog("[cefCreateBrowserInWidget] gtkWindow is nil")
		return nil
	}
	if url == "" {
		url = "about:blank"
	}

	stub := &cefClientStub{}
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
