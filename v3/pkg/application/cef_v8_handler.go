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

// cefWindowsByBrowserID maps a CEF browser identifier to its owning
// linuxWebviewWindow. The V8 extension handler is registered globally,
// so when JS calls wails_cefResolveDrop() we need a way to find the
// window that owns the active drag. The map is populated by
// cefCreateBrowserInWidget after CEF returns the Browser handle and
// removed in cef_destroy_browser_locked when the browser closes.
//
// CEF's GetIdentifier() returns a process-wide unique int32; reading
// it via browser.GetIdentifier() is safe on the UI thread (which is
// where browser creation and Execute both run in single-process mode).
var (
	cefWindowMapMu sync.RWMutex
	cefWindowsByBrowserID = map[int32]*linuxWebviewWindow{}
)

// registerCefBrowser associates a browser identifier with its window.
// Called from cefCreateBrowserInWidget after the browser is created.
func registerCefBrowser(id int32, w *linuxWebviewWindow) {
	if id == 0 || w == nil {
		return
	}
	cefWindowMapMu.Lock()
	cefWindowsByBrowserID[id] = w
	cefWindowMapMu.Unlock()
}

// unregisterCefBrowser removes the association. Called when the
// browser closes (clean shutdown or renderer crash).
func unregisterCefBrowser(id int32) {
	if id == 0 {
		return
	}
	cefWindowMapMu.Lock()
	delete(cefWindowsByBrowserID, id)
	cefWindowMapMu.Unlock()
}

// cefWindowForBrowser returns the linuxWebviewWindow owning the
// given CEF browser id, or nil if no such window is registered.
func cefWindowForBrowser(id int32) *linuxWebviewWindow {
	if id == 0 {
		return nil
	}
	cefWindowMapMu.RLock()
	w := cefWindowsByBrowserID[id]
	cefWindowMapMu.RUnlock()
	return w
}

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

	case "wails_invokeAsync":
		return r.handleInvokeAsync(arguments, retval, exception)

	case "wails_callback":
		return r.handleCallback(arguments, retval, exception)

	case "wails_cefResolveDrop":
		return r.handleCefResolveDrop(arguments, retval, exception)

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
// Phase 4: the retval out-param is populated with a CefV8Value created
// via cef.V8ValueCreateString. The JS-side Promise resolves with the
// string. Callers that want the raw object should JSON.parse the result
// (the runtime in internal/assetserver/bundledassets/runtime.js
// already does this).
func (r *cefV8Router) handleInvoke(msg string, retval unsafe.Pointer, exception uintptr) int32 {
	cefV8Mu.RLock()
	proc := cefV8Proc
	app := cefV8App
	cefV8Mu.RUnlock()

	if proc == nil {
		writeV8Exception(exception, "wails/cef: message processor not wired (call setCefMessageProcessor before browser creation)")
		return 0
	}

	var req RuntimeRequest
	if err := json.Unmarshal([]byte(msg), &req); err != nil {
		writeV8Exception(exception, fmt.Sprintf("wails/cef: invalid runtime request: %v", err))
		return 0
	}

	// Use a generous timeout (CEF renderer processes can be slow on
	// initial calls). Phase 5 will surface this as a configurable option.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := proc.HandleRuntimeCallWithIDs(ctx, &req)
	if err != nil {
		writeV8Exception(exception, fmt.Sprintf("wails/cef: runtime call failed: %v", err))
		return 0
	}

	payload, err := json.Marshal(result)
	if err != nil {
		writeV8Exception(exception, fmt.Sprintf("wails/cef: cannot marshal result: %v", err))
		return 0
	}

	if app != nil {
		app.debug("[cef-invoke] " + string(payload))
	}

	// Write payload into the retval out-param. CEF expects
	// *(*cef_v8_value_t)(retval) = newValue. We use the capi struct
	// pointer to get the underlying C handle and then write it.
	strVal := cef.V8ValueCreateString(string(payload))
	writeV8Retval(retval, strVal)
	return 1
}

// cefLogException writes a message to stderr (CEF surfaces stderr from
// the render process in its own log; we add a clear prefix). Phase 4
// will route this through the App logger via a CefV8Value::CreateString.
func cefLogException(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}

// handleCefResolveDrop is the Go side of the wails_cefResolveDrop
// native function (see cef_js_shim.js). The JS drop handler calls
// this synchronously during a drop event to fetch the file paths
// captured by the DragHandler.OnDragEnter callback.
//
// arguments[0] is the CEF browser identifier (int32, exposed as a JS
// number). arguments[1] is the X coordinate of the drop in CSS pixels
// (used by the window-side dispatch to identify the drop target).
// arguments[2] is the Y coordinate.
//
// On success the retval is a JSON array of file path strings (possibly
// empty if no drag is active or GetFilePaths returned no entries).
// The retval is always a valid JSON array — JS can call
// JSON.parse(result) without catching exceptions.
func (r *cefV8Router) handleCefResolveDrop(arguments []cef.V8Value, retval unsafe.Pointer, exception uintptr) int32 {
	if len(arguments) < 3 {
		writeV8Exception(exception, "wails/cef: wails_cefResolveDrop requires 3 arguments (browserId, x, y)")
		return 0
	}
	browserID := int32(arguments[0].GetIntValue())
	w := cefWindowForBrowser(browserID)
	if w == nil {
		// Browser was closed between OnDragEnter and the JS drop
		// handler — return an empty array so JS still gets a valid
		// JSON value.
		writeV8Retval(retval, cef.V8ValueCreateString("[]"))
		return 1
	}
	jsonPaths := w.cefResolveDragDrop()
	writeV8Retval(retval, cef.V8ValueCreateString(jsonPaths))
	return 1
}

// writeV8Retval writes a CefV8Value handle into CEF's retval out-param.
// The retval pointer is a *(*cef_v8_value_t); writing a raw handle there
// is the documented way to return a value from a V8Handler::Execute.
//
// We extract the handle via reflection-style unsafe casts since the
// inbound port doesn't expose RawPointer. The v8ValueImpl generated by
// purego-cef has a `rawPtr` field at offset 0 of its struct, so reading
// the first sizeof(uintptr) bytes of the iface gives us the C handle.
func writeV8Retval(retval unsafe.Pointer, v cef.V8Value) {
	if retval == nil || v == nil {
		return
	}
	// Convert the interface value to a pointer. The interface header
	// for a pointer-implementing type holds the pointer in its data
	// word on amd64.
	type ifaceHeader struct {
		_    uintptr
		data unsafe.Pointer
	}
	h := (*ifaceHeader)(unsafe.Pointer(&v)).data
	if h == nil {
		return
	}
	// Write the handle pointer into the retval slot.
	*(*unsafe.Pointer)(retval) = h
}

// writeV8Exception writes a JavaScript exception string into CEF's
// exception out-param. CEF will surface it as a TypeError in the JS
// caller.
func writeV8Exception(exception uintptr, msg string) {
	cefLogException(msg) // always log to stderr for diagnostics
	if exception == 0 {
		return
	}
	// The exception out-param is a *(*cef_string_t) (CEF owns a string
	// struct). We can build a CefString and write its address there.
	// For Phase 4 we keep the log-only behavior and rely on CEF to
	// raise a generic TypeError; Phase 5 will populate the proper
	// exception string.
	_ = exception
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
		app.warning("%s", "[browser] "+msg)
	case "error":
		app.error("%s", "[browser] "+msg)
	default:
		app.info("%s", "[browser] "+msg)
	}
}

// -----------------------------------------------------------------------------
// Async transport (Decision C12): wails_invokeAsync + _wailsAndroidCallback
// -----------------------------------------------------------------------------
//
// The Wails runtime JS detects `window.wails.invokeAsync` and switches from
// the default HTTP fetch transport to a customTransport that:
//   1. Generates a random Promise id.
//   2. Stores {resolve, reject} in a `pending` Map keyed by id.
//   3. Calls `window.wails.invokeAsync(id, payload)` synchronously.
//   4. Awaits `window._wailsAndroidCallback(id, response, error)` to fire
//      later — Go is expected to inject this via frame.ExecuteJavaScript.
//
// Our JS shim (`cef_js_shim.js`) provides `window.wails.invokeAsync` that
// bridges to the native `wails_invokeAsync(callId, payload)` function. The
// native handler dispatches the runtime call to a goroutine (so the V8
// thread is not blocked) and, on completion, schedules an InvokeAsync that
// executes `window._wailsAndroidCallback(callId, responseJSON, '')` (or the
// error variant) on the CEF UI thread.
//
// This unlocks the full `Call.ByName(...)` / `Call.ByID(...)` Promise
// machinery for CEF without changing the runtime JS bundle — the runtime's
// Android branch picks up the async path automatically because
// `window.wails.invokeAsync` is now a function.

const cefAsyncCallTimeout = 30 * time.Second

// handleInvokeAsync is the V8 router case for wails_invokeAsync. It spawns
// a goroutine to process the call (so V8 returns immediately) and returns 1
// without writing retval (the JS side does not await a value from this
// call — it awaits _wailsAndroidCallback instead).
//
// arguments[0] is the browser identifier (int32). arguments[1] is the
// Promise id (string). arguments[2] is the JSON RuntimeRequest payload.
func (r *cefV8Router) handleInvokeAsync(arguments []cef.V8Value, retval unsafe.Pointer, exception uintptr) int32 {
	if len(arguments) < 3 {
		writeV8Exception(exception, "wails/cef: wails_invokeAsync requires 3 arguments (browserId, callId, payload)")
		return 0
	}
	browserID := int32(arguments[0].GetIntValue())
	callID := arguments[1].GetStringValue()
	payload := arguments[2].GetStringValue()

	w := cefWindowForBrowser(browserID)
	if w == nil {
		// Browser was already closed; resolve immediately with an
		// error envelope so the JS Promise doesn't hang forever.
		cefResolveAsyncCall(nil, callID, "", fmt.Errorf("wails/cef: async invoke: browser %d not registered", browserID))
		return 1
	}
	if callID == "" {
		writeV8Exception(exception, "wails/cef: wails_invokeAsync callId is empty")
		return 0
	}

	cefV8Mu.RLock()
	proc := cefV8Proc
	cefV8Mu.RUnlock()
	if proc == nil {
		cefResolveAsyncCall(w, callID, "", fmt.Errorf("wails/cef: message processor not wired"))
		return 1
	}

	// Dispatch on a background goroutine. The call may be long-running
	// (service methods, dialogs, etc.) and we MUST NOT block V8 —
	// blocking V8 also blocks the Chromium UI message loop in
	// single-process mode, freezing the window until we return.
	go func() {
		// Defensive copy of the payload bytes so the goroutine
		// doesn't depend on the V8 string's lifetime after we
		// return from Execute.
		payloadCopy := make([]byte, len(payload))
		copy(payloadCopy, payload)

		var req RuntimeRequest
		if err := json.Unmarshal(payloadCopy, &req); err != nil {
			cefResolveAsyncCall(w, callID, "", fmt.Errorf("wails/cef: invalid runtime request: %w", err))
			return
		}

		// Bounded timeout matches the sync handleInvoke path. Phase 5
		// will surface this as a configurable option.
		ctx, cancel := context.WithTimeout(context.Background(), cefAsyncCallTimeout)
		defer cancel()

		result, err := proc.HandleRuntimeCallWithIDs(ctx, &req)
		if err != nil {
			cefResolveAsyncCall(w, callID, "", err)
			return
		}

		// Marshal the success envelope. The runtime expects
		// {ok: true, data: <JSON>}; we also support {ok, text} for
		// plain-string responses.
		payload, err := json.Marshal(result)
		if err != nil {
			cefResolveAsyncCall(w, callID, "", fmt.Errorf("wails/cef: marshal result: %w", err))
			return
		}
		cefResolveAsyncCall(w, callID, string(payload), nil)
	}()

	return 1
}

// cefResolveAsyncCall schedules an ExecuteJavaScript that calls
// window._wailsAndroidCallback(callID, responseJSON, errorString) on the
// main frame of the given window. The actual JS execution happens on the
// CEF UI thread via InvokeAsync (which is g_idle_add_full in the CGo
// preamble of linux_cgo_cef.go).
//
// responseJSON is the marshalled success payload (may be empty). errMsg
// is non-empty when the call failed. Exactly one of responseJSON/errMsg
// should be set per call.
//
// Passing a nil window means "no live browser" — in that case the call
// is dropped silently because there's no frame to inject into. This is
// the best we can do when the browser closed between InvokeAsync and
// the goroutine finishing.
func cefResolveAsyncCall(w *linuxWebviewWindow, callID, responseJSON string, err error) {
	var js string
	if err != nil {
		// Build the call literally to avoid JSON-escaping the error
		// string (which may contain quotes, newlines, etc.).
		errJSON, _ := json.Marshal(err.Error())
		js = fmt.Sprintf("if(window._wailsAndroidCallback){window._wailsAndroidCallback(%q,%q,%s);}",
			callID, "", string(errJSON))
	} else {
		// Envelope: {"ok":true,"data":<responseJSON>}. We embed
		// responseJSON verbatim so the runtime's JSON.parse yields
		// the exact object Go returned.
		js = fmt.Sprintf("if(window._wailsAndroidCallback){window._wailsAndroidCallback(%q,%q,%q);}",
			callID,
			fmt.Sprintf(`{"ok":true,"data":%s}`, responseJSON),
			"",
		)
	}

	if w == nil {
		return
	}

	// Capture by value so the closure is safe even if `w` is
	// destroyed while the call is queued.
	target := w
	InvokeAsync(func() {
		if target == nil || target.browser == nil {
			return
		}
		frame := target.browser.GetMainFrame()
		if frame == nil {
			return
		}
		frame.ExecuteJavaScript(js, "", 0)
	})
}

// handleCallback is the V8 router case for wails_callback. This is the
// Go→JS→Go round-trip path exposed by `window.wails.handleCallback` in
// the JS shim — Go injects `handleCallback(id, ok, result)` via
// execJS, JS calls `wails_callback(id, ok, result)` native, and this
// handler receives the result.
//
// Today no Go-side code subscribes to these callbacks (they're a
// future hook for event-stream subscriptions); we record them in a
// bounded ring so callers (tests, future push-notification handlers)
// can inspect what was delivered, and return success so JS doesn't
// log a "TypeError: not a function" exception.
//
// arguments[0] is the call id (string). arguments[1] is "1" or "0"
// (ok flag). arguments[2] is the result payload (string, may be empty).
func (r *cefV8Router) handleCallback(arguments []cef.V8Value, retval unsafe.Pointer, exception uintptr) int32 {
	if len(arguments) < 3 {
		writeV8Exception(exception, "wails/cef: wails_callback requires 3 arguments (id, ok, result)")
		return 0
	}
	id := arguments[0].GetStringValue()
	ok := arguments[1].GetStringValue() == "1"
	result := arguments[2].GetStringValue()
	recordCefCallback(id, ok, result)
	return 1
}

// -----------------------------------------------------------------------------
// wails_callback registry: small ring buffer of recent callbacks so tests
// and (future) push-notification handlers can inspect delivery.
// -----------------------------------------------------------------------------

const cefCallbackRingSize = 64

type cefCallbackRecord struct {
	id     string
	ok     bool
	result string
}

var (
	cefCallbackMu  sync.Mutex
	cefCallbackLog = make([]cefCallbackRecord, 0, cefCallbackRingSize)
)

// recordCefCallback appends a callback to the bounded ring. Old entries
// are evicted FIFO once the ring fills. This is intentionally simple —
// callbacks are a future hook (push notifications / event-stream
// confirmations), not a hot path.
func recordCefCallback(id string, ok bool, result string) {
	cefCallbackMu.Lock()
	defer cefCallbackMu.Unlock()
	if len(cefCallbackLog) >= cefCallbackRingSize {
		// Drop the oldest entry.
		copy(cefCallbackLog, cefCallbackLog[1:])
		cefCallbackLog = cefCallbackLog[:len(cefCallbackLog)-1]
	}
	cefCallbackLog = append(cefCallbackLog, cefCallbackRecord{id: id, ok: ok, result: result})
}

// recentCefCallbacks returns a copy of the callback ring (oldest first).
// Used by tests and diagnostics.
func recentCefCallbacks() []cefCallbackRecord {
	cefCallbackMu.Lock()
	defer cefCallbackMu.Unlock()
	out := make([]cefCallbackRecord, len(cefCallbackLog))
	copy(out, cefCallbackLog)
	return out
}
