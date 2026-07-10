//go:build linux && cgo && cef && !android && !server

package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/bnema/purego-cef/cef"
)

// cefExtensionName is the unique identifier for our V8 extension. It
// must be globally unique across all extensions registered by CEF in
// the process.
const cefExtensionName = "wails.cef"

// globalCefV8Handler is the singleton V8 handler wired up to the
// message processor. It is created once per app via
// setCefMessageProcessor and lives for the lifetime of the process.
var (
	cefV8Mu      sync.RWMutex
	cefV8App     *App
	cefV8Proc    *MessageProcessor
	cefV8Handler cef.V8Handler

	cefExtOnce sync.Once
)

// setCefMessageProcessor wires the V8 handler to the App's
// messageProcessor so that calls from JS land in the same router that
// HTTP and WebSocket transports use. Called from newPlatformApp.
func setCefMessageProcessor(app *App) {
	cefV8Mu.Lock()
	defer cefV8Mu.Unlock()
	cefV8App = app
	cefV8Proc = app.messageProcessor
	if cefV8Handler == nil {
		cefV8Handler = cef.NewV8Handler(&cefV8Router{})
	}
}

// registerCEFExtension installs the "wails.cef" V8 extension that exposes
// window.wails.* native functions. Called once from application startup
// (BEFORE any CefBrowser is created; CEF only loads registered
// extensions in browsers spawned after the call).
func registerCEFExtension() {
	cefExtOnce.Do(func() {
		cef.RegisterExtension(cefExtensionName, cefJSShim(), getCefV8HandlerOrNew())
	})
}

func getCefV8HandlerOrNew() cef.V8Handler {
	cefV8Mu.RLock()
	h := cefV8Handler
	cefV8Mu.RUnlock()
	if h != nil {
		return h
	}
	return cef.NewV8Handler(&cefV8Router{})
}

// -----------------------------------------------------------------------------
// cefV8Router: implements cef.V8Handler
// -----------------------------------------------------------------------------

// cefV8Router dispatches V8 Execute calls to the appropriate Go-side
// handler. The "name" parameter is the native function name from the
// JS shim (e.g. "wails_invoke", "wails_callback", "wails_log").
type cefV8Router struct{}

// Execute is called by CEF when JS calls one of the native functions
// registered in cef_js_shim.js. The retval/out-param must be set to a
// CefV8Value to return a value to JS. exception/out-param should be set
// to a string when the call fails.
//
// Phase 3 supports "wails_invoke" (the main RPC path) and stubs the rest.
// Phase 4 will implement async callback resolution (Promise resolve from
// Go back into JS) and proper retval/exception marshalling.
func (r *cefV8Router) Execute(name string, _ cef.V8Value, arguments []cef.V8Value, retval unsafe.Pointer, exception uintptr) int32 {
	if len(arguments) == 0 {
		cefLogException(fmt.Sprintf("wails/cef: native function '%s' requires at least 1 argument", name))
		return 0
	}

	switch name {
	case "wails_invoke":
		return r.handleInvoke(arguments[0].GetStringValue(), retval, exception)

	case "wails_callback":
		// Phase 3 stub. Async callback resolution lands in Phase 4.
		return 1

	case "wails_log":
		if len(arguments) >= 2 {
			logCefConsole(arguments[0].GetStringValue(), arguments[1].GetStringValue())
		}
		return 1

	case "wails_setFlags":
		// Phase 3 stub; flags are pushed from Go via ExecJS in Phase 4.
		return 1

	case "wails_setEnvironment":
		// Phase 3 stub; environment is pushed similarly in Phase 4.
		return 1

	case "wails_emit":
		// Phase 3 stub.
		return 1
	}

	cefLogException(fmt.Sprintf("wails/cef: unknown native function '%s'", name))
	return 0
}

// handleInvoke processes an IPC call from JS. The msg argument is a
// JSON-encoded RuntimeRequest (the same shape HTTP/WS transports send).
// We deserialize, route through MessageProcessor, and return the result
// as a JSON-encoded value via the retval out-param.
//
// Phase 3 limitation: returning the value to JS via retval out-param
// requires CefV8Value::CreateString + the CEF retain dance, which is
// not exposed via purego-cef's inbound port. For now we log the result
// and return 1 (handled); the JS-side Promise resolves to undefined.
// Phase 4 will fix the return path.
func (r *cefV8Router) handleInvoke(msg string, retval unsafe.Pointer, exception uintptr) int32 {
	cefV8Mu.RLock()
	proc := cefV8Proc
	app := cefV8App
	cefV8Mu.RUnlock()

	if proc == nil {
		cefLogException("wails/cef: message processor not wired (call setCefMessageProcessor before browser creation)")
		return 0
	}

	var req RuntimeRequest
	if err := json.Unmarshal([]byte(msg), &req); err != nil {
		cefLogException(fmt.Sprintf("wails/cef: invalid runtime request: %v", err))
		return 0
	}

	// Use a generous timeout (CEF renderer processes can be slow on
	// initial calls). Phase 4 will surface this as a configurable option.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := proc.HandleRuntimeCallWithIDs(ctx, &req)
	if err != nil {
		cefLogException(fmt.Sprintf("wails/cef: runtime call failed: %v", err))
		return 0
	}

	// Marshal the result and log it. Returning to JS via retval is
	// Phase 4.
	payload, err := json.Marshal(result)
	if err != nil {
		cefLogException(fmt.Sprintf("wails/cef: cannot marshal result: %v", err))
		return 0
	}

	if app != nil && app.options.Logger != nil {
		// Verbose debug log; not noisy at info level.
		app.debug("[cef-invoke] " + string(payload))
	}

	// Phase 4: write payload into retval via CefV8Value::CreateString
	// + cef_v8_value_set_retval. Until then, return 1 (handled).
	_ = retval
	_ = exception
	return 1
}

// cefLogException writes a message to stderr (CEF surfaces stderr from
// the render process in its own log; we add a clear prefix). Phase 4
// will route this through the App logger via a CefV8Value::CreateString.
func cefLogException(msg string) {
	fmt.Fprintf(os.Stderr, msg+"\n")
}

// logCefConsole forwards browser console messages to the App's logger.
func logCefConsole(level, msg string) {
	cefV8Mu.RLock()
	app := cefV8App
	cefV8Mu.RUnlock()
	if app == nil {
		fmt.Fprintf(os.Stderr, "wails/cef[console:%s]: %s\n", level, msg)
		return
	}
	switch level {
	case "warn":
		app.warning("[browser] " + msg)
	case "error":
		app.error("[browser] " + msg)
	default:
		app.info("[browser] " + msg)
	}
}