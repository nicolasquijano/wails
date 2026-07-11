//cef_js_shim.js — JS code injected by CEF's RegisterExtension.
// This runs BEFORE any page script. It defines native functions on the
// 'wails' global object that the runtime JS in internal/assetserver/
// bundledassets/runtime.js expects to find.
//
// The runtime expects window.wails.invoke(json), which ultimately posts
// to window.chrome.webview.postMessage(json). CEF does NOT expose that
// API, so we shim it: when window.chrome.webview is missing but
// window.wails.invoke exists, the runtime uses the latter directly.
//
// Function mapping (native function → V8Handler.Execute name):
//   wails_invoke(json)                  → "invoke" (sync, blocks until Go returns)
//   wails_invokeAsync(id, json)         → "invokeAsync" (non-blocking; Go resolves
//                                          via window._wailsAndroidCallback later)
//   wails_callback(callId, ok, result)  → "callback"
//   wails_log(level, msg)               → "log"
//   wails_setFlags(flagsJSON)           → "setFlags"
//   wails_setEnvironment(envJSON)       → "setEnvironment"
//   wails_emit(name, dataJson)          → "emit"
//   wails_cefResolveDrop(id, x, y)      → "cefResolveDrop"
//
// SECURITY MODEL (see Decision C14 in CEF_IMPLEMENTATION.md):
//
//   The native function bindings (`native function wails_invoke`) are
//   scoped to the IIFE that wraps this file. V8 enforces lexical
//   scoping on native bindings (see CEF docs: "The calling of a native
//   function is restricted to the scope in which the prototype of the
//   native function is defined"). Page scripts — including anything
//   reachable via DevTools — cannot call wails_invoke directly; they
//   can only call the JS wrappers on window.wails / window._wails
//   that we expose below.
//
//   The JS wrappers are the *only* attack surface from the page. We
//   harden them in three places:
//     1. Input validation: every wrapper checks argument shape and
//        rejects non-string/empty payloads before touching the native.
//     2. Optional method allow-list (window._wails.cefAllowedMethods):
//        when Go injects a non-empty list, the wrapper rejects
//        Call.ByName(...) calls whose `methodName` is not in the
//        list. Built-in subsystems (Window, Events, System, …) are
//        always allowed because the runtime uses them internally.
//     3. Optional CSRF nonce (window._wails.cefEnforceNonce + .cefNonce):
//        when enforce=1 and a nonce is set, every call must carry the
//        nonce as the second arg of window.wails.invoke. The wrapper
//        strips the nonce before forwarding to the native binding
//        so the runtime bundle (which doesn't know about nonces)
//        keeps working.

(function() {
  if (!window.wails) window.wails = {};

  // ── Native bindings ──────────────────────────────────────────────
  // Scoped to this IIFE. Page scripts cannot call these directly.
  native function wails_invoke(msg);
  native function wails_invokeAsync(callId, payload);
  native function wails_callback(id, ok, result);
  native function wails_log(level, msg);
  native function wails_setFlags(flagsJSON);
  native function wails_setEnvironment(envJSON);
  native function wails_emit(name, dataJson);
  native function wails_cefResolveDrop(id, x, y);

  // ── Configuration injected by Go on every navigation ────────────
  // Read lazily so the wrapper picks up changes if Go injects later
  // (it does — flags/env come in two phases in OnDocumentAvailableInMainFrame
  // and OnLoadEnd). Defaults are open; presence of values activates the
  // corresponding hardening path.
  function getCefAllowed() {
    var a = (window._wails && window._wails.cefAllowedMethods) || null;
    if (!a || typeof a.indexOf !== "function") return null;
    return a;
  }
  function getCefNonce() {
    if (!(window._wails && window._wails.cefEnforceNonce)) return "";
    return (window._wails && window._wails.cefNonce) || "";
  }

  // Built-in subsystem method names are always allowed regardless of
  // the user-supplied allow-list. These are the ones the runtime bundle
  // (internal/runtime/desktop/@wailsio/runtime/src/runtime.ts) calls
  // internally; locking them out would break the runtime.
  var BUILTIN_METHOD_PREFIXES = [
    "Window.",
    "Events.",
    "System.",
    "Screens.",
    "Clipboard.",
    "Browser.",
    "Dialogs.",
    "Application.",
    "CancelCall.",
    "IOS.",
    "Android.",
  ];
  function isBuiltin(methodName) {
    if (!methodName) return false;
    for (var i = 0; i < BUILTIN_METHOD_PREFIXES.length; i++) {
      if (methodName.indexOf(BUILTIN_METHOD_PREFIXES[i]) === 0) return true;
    }
    return false;
  }

  // ── Sync wrapper ─────────────────────────────────────────────────
  // window.wails.invoke(msg, [nonce]) — the runtime calls this with
  // a single string-or-object arg. We accept an optional second arg
  // for the CSRF nonce (so manual callers can opt in); when nonce
  // enforcement is on and the caller's nonce doesn't match we silently
  // return undefined rather than calling the native binding.
  function wailsInvoke(msg, providedNonce) {
    if (msg === null || typeof msg === "undefined") return undefined;
    var payload;
    if (typeof msg === "string") {
      payload = msg;
    } else {
      try { payload = JSON.stringify(msg); }
      catch (e) { console.error("wails/cef: invoke payload not JSON-serializable", e); return undefined; }
    }
    // Allow-list check (only on object payloads that have methodName —
    // sync returns don't carry it; allow-list applies to Call.ByName).
    var nonce = getCefNonce();
    if (nonce) {
      if (providedNonce !== nonce) {
        console.warn("wails/cef: invoke rejected — invalid CSRF nonce");
        return undefined;
      }
    }
    // When the payload is a JSON object (not a string), try to inspect
    // its methodName so we can enforce the allow-list before invoking.
    if (typeof msg === "object" && getCefAllowed()) {
      try {
        var parsed = JSON.parse(payload);
        if (parsed && typeof parsed.methodName === "string") {
          if (!isBuiltin(parsed.methodName) &&
              getCefAllowed().indexOf(parsed.methodName) === -1) {
            console.warn("wails/cef: invoke rejected — method not in allow-list:", parsed.methodName);
            return undefined;
          }
        }
      } catch (e) { /* not a JSON object; fall through */ }
    }
    try {
      return wails_invoke(payload);
    } catch (e) {
      console.error("wails/cef: native invoke threw", e);
      return undefined;
    }
  }
  window.wails.invoke = wailsInvoke;

  // ── Async wrapper ────────────────────────────────────────────────
  function wailsInvokeAsync(callId, payload, providedNonce) {
    if (typeof callId !== "string") callId = callId == null ? "" : String(callId);
    if (payload === null || typeof payload === "undefined") payload = "";
    else if (typeof payload !== "string") {
      try { payload = JSON.stringify(payload); }
      catch (e) { console.error("wails/cef: invokeAsync payload not JSON-serializable", e); return; }
    }
    var nonce = getCefNonce();
    if (nonce && providedNonce !== nonce) {
      console.warn("wails/cef: invokeAsync rejected — invalid CSRF nonce");
      return;
    }
    // Allow-list check on async Call.ByName payloads.
    if (typeof payload === "string" && payload.charAt(0) === "{" && getCefAllowed()) {
      try {
        var parsed = JSON.parse(payload);
        if (parsed && typeof parsed.methodName === "string" &&
            !isBuiltin(parsed.methodName) &&
            getCefAllowed().indexOf(parsed.methodName) === -1) {
          console.warn("wails/cef: invokeAsync rejected — method not in allow-list:", parsed.methodName);
          return;
        }
      } catch (e) { /* not a JSON object; fall through */ }
    }
    try {
      wails_invokeAsync(callId, payload);
    } catch (e) {
      console.error("wails/cef: native invokeAsync threw", e);
    }
  }
  window.wails.invokeAsync = wailsInvokeAsync;

  // The runtime expects to be able to RECEIVE async callbacks from Go.
  // wails_callback is invoked by Go via window.wails.handleCallback(id, ok, result)
  // (see internal/runtime/cef_bridge.go for the Go side).
  window.wails.handleCallback = function(id, ok, result) {
    try { wails_callback(id, ok ? "1" : "0", result == null ? "" : result); }
    catch (e) { console.error("wails handleCallback failed:", e); }
  };

  // Expose _wails for the existing runtime that uses both namespaces.
  window._wails = window._wails || {};
  window._wails.invoke = window.wails.invoke;

  // CEF-specific drag-drop bridge. The runtime JS in runtime.js installs
  // dragenter/dragover/dragleave/drop listeners on documentElement and
  // resolves file paths via window.chrome.webview.postMessageWithAdditionalObjects
  // (a WebView2-only API). CEF doesn't have that, so on the drop event
  // we fall back to the native wails_cefResolveDrop function, which
  // returns the file paths captured by the DragHandler.OnDragEnter
  // callback in Go. We call _wails.handlePlatformFileDrop ourselves
  // with the recovered paths so the existing runtime keeps working.
  //
  // The runtime JS unconditionally attaches the standard drop listener
  // when enableFileDrop is true. That listener will see our drops and
  // try to use WebView2's API; if that API isn't present, the runtime
  // falls back to _wails.handlePlatformFileDrop only when the drop was
  // triggered by native code (e.g. GTK4's drag/drop). For CEF, the
  // native call happens via the C++ DragHandler, not the runtime's
  // built-in path, so we install our own capture-phase drop handler
  // to do the resolution and forward to handlePlatformFileDrop.
  function _wailsCefDndSetup() {
    if (window._wailsCefDndSetupDone) return;
    window._wailsCefDndSetupDone = true;
    // browserId is set on first OnDocumentAvailableInMainFrame call
    // (see cef_request_handler.go). Until then, _wailsCefBrowserId is 0
    // and wails_cefResolveDrop returns "[]" (no drag active).
    window._wailsCefBrowserId = window._wailsCefBrowserId || 0;
    document.addEventListener('drop', function(ev) {
      try {
        if (!ev.dataTransfer || !ev.dataTransfer.files || ev.dataTransfer.files.length === 0) return;
        if (window._wails && window._wails.flags && window._wails.flags.enableFileDrop === false) return;
        var dt = ev.dataTransfer;
        var x = ev.clientX || 0;
        var y = ev.clientY || 0;
        var raw = wails_cefResolveDrop(window._wailsCefBrowserId, x, y);
        var paths = [];
        try { paths = JSON.parse(raw); } catch (e) { paths = []; }
        if (!paths || paths.length === 0) return;
        ev.preventDefault();
        ev.stopPropagation();
        if (window.wails && typeof window.wails.handlePlatformFileDrop === 'function') {
          window.wails.handlePlatformFileDrop(paths, x, y);
        } else if (window._wails && typeof window._wails.handlePlatformFileDrop === 'function') {
          window._wails.handlePlatformFileDrop(paths, x, y);
        }
      } catch (e) {
        try { console.error('wails cef dnd drop handler:', e); } catch (_) {}
      }
    }, true /* useCapture: run before the runtime's bubble-phase handler */);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', _wailsCefDndSetup, { once: true });
  } else {
    _wailsCefDndSetup();
  }

  // Stash initial empty flags/env; Go will populate them shortly.
  window._wails.flags = window._wails.flags || {};
  window._wails.environment = window._wails.environment || {};

  // Console relay: pipe browser console messages into Go's logger.
  try {
    var origLog = console.log, origWarn = console.warn, origErr = console.error;
    console.log = function() {
      try { wails_log("info", Array.prototype.slice.call(arguments).map(stringify_).join(" ")); }
      catch (e) {}
      origLog.apply(console, arguments);
    };
    console.warn = function() {
      try { wails_log("warn", Array.prototype.slice.call(arguments).map(stringify_).join(" ")); }
      catch (e) {}
      origWarn.apply(console, arguments);
    };
    console.error = function() {
      try { wails_log("error", Array.prototype.slice.call(arguments).map(stringify_).join(" ")); }
      catch (e) {}
      origErr.apply(console, arguments);
    };
    function stringify_(v) { try { return typeof v === "string" ? v : JSON.stringify(v); } catch (e) { return String(v); } }
  } catch (e) { /* console shim is best-effort */ }
})();