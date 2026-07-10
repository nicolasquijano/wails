//go:build linux && cgo && cef && !android && !server

package application

/*
#cgo pkg-config: gtk4
#cgo pkg-config: gio-unix-2.0

#include <gtk/gtk.h>
#include <gio/gio.h>
static guint get_compiled_gtk_major_version() { return gtk_get_major_version(); }
static guint get_compiled_gtk_minor_version() { return gtk_get_minor_version(); }
static guint get_compiled_gtk_micro_version() { return gtk_get_micro_version(); }

// (cef_activate_cb is defined in linux_cgo_cef.go's cgo block.)
*/
import "C"

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/internal/operatingsystem"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// appName and sanitizeAppName are CEF-build stubs (the real ones live in
// linux_cgo.go / application_linux.go which are excluded from -tags cef).
func appName() string {
	if globalApplication == nil {
		return ""
	}
	return globalApplication.options.Name
}

// sanitizeAppName converts a free-form app name into a string that
// GTK4's g_application_id_is_valid accepts. GTK4 / D-Bus require:
//
//   - lowercase only
//   - at least one '.' (reverse-DNS style)
//   - each component starts with [a-z]
//
// We always prefix with "io.wails." when the input doesn't already
// contain a dot, which matches the upstream webgtk default.
func sanitizeAppName(name string) string {
	if name == "" {
		return "io.wails.app"
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)

	if !strings.Contains(out, ".") {
		out = "io.wails." + out
	}
	if out == "" || !isValidAppIDStart(out[0]) {
		out = "io.wails." + out
	}
	return out
}

func isValidAppIDStart(c byte) bool {
	return c >= 'a' && c <= 'z'
}

func setProgramName(name string) { _ = name }

// init runs in every process, including CEF helper subprocesses (zygote,
// utility, gpu). It does the very first thing: check whether we are a CEF
// helper subprocess and exit immediately if so. Without this, every helper
// subprocess would also run the main() function, which calls App.Run,
// which calls newPlatformApp, which calls cefInit — and we end up with
// multiple CEF runtimes competing for the same process.
//
// In the main process this function is a no-op (cefInit hasn't been
// called yet, so the library detection returns false).
func init() {
	// Detect via os.Args: CEF helper subprocesses are spawned with
	// flags like --type=zygote, --type=utility, --type=gpu-process.
	// We exit before doing any other init.
	for _, a := range os.Args {
		switch a {
		case "--type=zygote", "--type=zygote-process", "--type=utility", "--type=gpu-process", "--type=renderer", "--type=broker", "--type=ppapi", "--type=ppapi-broker", "--type=audio-service", "--type=network-service", "--type=storage-service":
			fmt.Fprintf(os.Stderr, "wails/cef: detected CEF helper subprocess (%s), exiting\n", a)
			os.Exit(0)
		}
	}
}

// appRun and appDestroy are implemented in linux_cgo_cef.go.

func appDestroy(app pointer) {
	if app != nil {
		C.g_object_unref(C.gpointer(app))
	}
}

// linuxApp is the CEF-flavoured application backend.
//
// In Phase 1 this struct duplicates the GTK4 default fields so we can
// satisfy the platformApp interface (-tags cef). Phase 4 will refactor
// toward a shared linuxAppBase shared between webgtk and cef backends.
type linuxApp struct {
	application pointer
	parent      *App

	activated     chan struct{}
	activatedOnce sync.Once

	windowMap     map[windowPointer]uint
	windowMapLock sync.Mutex

	theme string

	icon pointer
}

func (a *linuxApp) GetFlags(options Options) map[string]any {
	if options.Flags == nil {
		options.Flags = make(map[string]any)
	}
	return options.Flags
}

func (a *linuxApp) name() string {
	return appName()
}

// run starts the GTK4 main loop with CEF pumped inline.
//
// Phase 1: minimal. Real implementation lands in Phase 2.
func (a *linuxApp) run() error {
	if err := cefInit(); err != nil {
		return fmt.Errorf("wails/cef: init failed: %w", err)
	}
	a.markActivated()
	defer cefShutdown()
	return appRun(a.application)
}

func (a *linuxApp) destroy() {
	if !globalApplication.shouldQuit() {
		return
	}
	globalApplication.cleanup()
	appDestroy(a.application)
}

func (a *linuxApp) getApplicationMenu() *Menu {
	return nil
}

func (a *linuxApp) setApplicationMenu(menu *Menu) {}

func (a *linuxApp) hide() {}

func (a *linuxApp) show() {}

func (a *linuxApp) on(eventID uint) {}

func (a *linuxApp) isOnMainThread() bool { return true }

func (a *linuxApp) appendGTKVersion(result map[string]string) {
	result["GTK"] = fmt.Sprintf("%d.%d.%d",
		C.get_compiled_gtk_major_version(),
		C.get_compiled_gtk_minor_version(),
		C.get_compiled_gtk_micro_version())
	result["WebKit"] = "n/a (CEF mode)"
	result["Chromium"] = "loaded via libcef.so"
}

func (a *linuxApp) init(_ *App, options Options) {
	osInfo, _ := operatingsystem.Info()
	a.parent.info("Compiled with GTK %d.%d.%d",
		C.get_compiled_gtk_major_version(),
		C.get_compiled_gtk_minor_version(),
		C.get_compiled_gtk_micro_version())
	a.parent.info("Using CEF backend (Chromium Embedded Framework)")
	a.parent.info("Using %s", osInfo.Name)

	if options.Icon != nil {
		a.setIcon(options.Icon)
	}
}

func (a *linuxApp) registerWindow(window pointer, id uint) {
	a.windowMapLock.Lock()
	a.windowMap[windowPointer(window)] = id
	a.windowMapLock.Unlock()
}

func (a *linuxApp) unregisterWindow(window windowPointer) {
	a.windowMapLock.Lock()
	delete(a.windowMap, window)
	remainingWindows := len(a.windowMap)
	a.windowMapLock.Unlock()

	if remainingWindows == 0 && !a.parent.options.Linux.DisableQuitOnLastWindowClosed {
		a.destroy()
	}
}

func newPlatformApp(parent *App) *linuxApp {
	name := sanitizeAppName(parent.options.Name)
	app := &linuxApp{
		parent:      parent,
		application: appNew(name),
		activated:   make(chan struct{}),
		windowMap:   map[windowPointer]uint{},
	}

	if parent.options.Linux.ProgramName != "" {
		setProgramName(parent.options.Linux.ProgramName)
	}

	// Initialize CEF FIRST. Every other CEF API (NewV8Handler,
	// RegisterExtension, etc.) requires the ref manager to be set up,
	// which only happens inside cef.Init(). Until Phase 6 we ignore the
	// error and let run() surface it; here we call it once so the
	// subsequent registrations work.
	debugLog("[newPlatformApp] start")
	if err := cefInit(); err != nil {
		debugLog("[newPlatformApp] cefInit failed: %v", err)
		parent.error("wails/cef: init failed (continuing, run() will surface): %v", err)
	}
	debugLog("[newPlatformApp] after cefInit")

	// Wire the assetserver handler into the CEF request pipeline so CEF
	// browsers can resolve wails:// URLs to embedded assets. This is
	// called once per app; safe to call multiple times (subsequent calls
	// just rebind the handler).
	if parent.assets != nil {
		setCefAssetsHandler(parent.assets)
	}

	// Wire the message processor into the CEF V8 handler so JS calls to
	// window.wails.* land in the same router as HTTP/WS transports.
	setCefMessageProcessor(parent)

	// Cache flags/environment for OnDocumentAvailableInMainFrame to
	// inject into the V8 context.
	setCefEnvironment(parent)

	// Install the V8 extension BEFORE any browser is created. CEF only
	// loads extensions that were registered before the browser's
	// render process started.
	registerCEFExtension()

	// Register the "wails" custom scheme so CEF recognises wails://
	// URLs as valid (otherwise the browser shows a blank page because
	// the URL parser rejects the unknown scheme before any request fires).
	registerWailsScheme()

	return app
}

func (a *linuxApp) markActivated() {
	a.activatedOnce.Do(func() {
		close(a.activated)
	})
}

func (a *linuxApp) waitForActivation() {
	<-a.activated
}

func (a *linuxApp) getIconForFile(filename string) ([]byte, error) {
	if filename == "" {
		return nil, nil
	}
	return nil, nil
}

func (a *linuxApp) isDarkMode() bool { return false }

func (a *linuxApp) getAccentColor() string {
	return "rgb(0,122,255)"
}

func (a *linuxApp) isVisible() bool { return true }

func (a *linuxApp) getWindows() []pointer {
	a.windowMapLock.Lock()
	defer a.windowMapLock.Unlock()
	out := make([]pointer, 0, len(a.windowMap))
	for w := range a.windowMap {
		out = append(out, pointer(w))
	}
	return out
}

func (a *linuxApp) setIcon(icon []byte) { _ = icon }

func (a *linuxApp) showAboutDialog(title, message string, icon []byte) {
	_, _ = title, message
	_ = icon
}

func (a *linuxApp) getCurrentWindowID() uint { return 0 }

func (a *linuxApp) getPrimaryScreen() (*Screen, error) { return &Screen{}, nil }

func (a *linuxApp) getScreens() ([]*Screen, error) { return nil, nil }

func getNativeApplication() *linuxApp {
	return globalApplication.impl.(*linuxApp)
}

// dispatchOnMainThread runs `fn` on the GTK main thread. In Phase 1 we
// just call fn synchronously since CEF and GTK share the same main OS
// thread and we don't pump the GTK loop from inside this file.
func (a *linuxApp) dispatchOnMainThread(id uint) {
	InvokeSync(func() {})
	_ = id
}

// processAndCacheScreens, setupCommonEvents, monitorPowerEvents, hideAllWindows,
// showAllWindows, isOnMainThread helpers are Phase 1 stubs (the real ones live
// in screen_linux.go / application_linux.go which we exclude from -tags cef).
func (a *linuxApp) processAndCacheScreens() error { return nil }
func (a *linuxApp) setupCommonEvents()             {}
func (a *linuxApp) hideAllWindows()                {}
func (a *linuxApp) showAllWindows()                {}

// logPlatformInfo / platformEnvironment are Phase 1 stubs (real implementations
// live in application_linux.go / environment_manager.go; we exclude those).
func (a *App) logPlatformInfo() {}

func (a *App) platformEnvironment() map[string]any {
	return map[string]any{
		"gtk4-compiled":   fmt.Sprintf("%d.%d.%d", C.get_compiled_gtk_major_version(), C.get_compiled_gtk_minor_version(), C.get_compiled_gtk_micro_version()),
		"chromium":        "loaded via libcef.so",
		"cef-min-version": 147,
		"webview":         "cef",
	}
}

// listenForSystemThemeChangesCEF is the CEF build's D-Bus theme watcher.
// It emits a "wails:theme:changed" event whenever the portal reports a
// change in `org.freedesktop.appearance::color-scheme`.
func listenForSystemThemeChangesCEF(a *linuxApp) {
	conn, err := dbus.SessionBus()
	if err != nil {
		a.parent.error("failed to connect to session bus: %v", err)
		return
	}

	if err = conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.portal.Settings"),
		dbus.WithMatchMember("SettingChanged"),
	); err != nil {
		return
	}

	c := make(chan *dbus.Signal, 10)
	conn.Signal(c)

	for s := range c {
		if len(s.Body) < 3 {
			continue
		}
		namespace, ok := s.Body[0].(string)
		if !ok || namespace != "org.freedesktop.appearance" {
			continue
		}
		key, ok := s.Body[1].(string)
		if !ok || key != "color-scheme" {
			continue
		}
		a.theme = "system"
		a.parent.Event.Emit("wails:theme:changed", a.isDarkMode())
	}
}

func fatalHandler(errFunc func(error)) { _ = errFunc }

// silence unused import.
var _ = events.Common
var _ = sync.Mutex{}
var _ = dbus.SessionBus