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
//   wails_invoke(json)                  → "invoke"
//   wails_callback(callId, ok, result)  → "callback"
//   wails_log(level, msg)               → "log"
//   wails_setFlags(flagsJSON)           → "setFlags"
//   wails_setEnvironment(envJSON)       → "setEnvironment"
//   wails_emit(name, dataJson)          → "emit"

(function() {
  if (!window.wails) window.wails = {};

  native function wails_invoke(msg);
  native function wails_callback(id, ok, result);
  native function wails_log(level, msg);
  native function wails_setFlags(flagsJSON);
  native function wails_setEnvironment(envJSON);
  native function wails_emit(name, dataJson);

  // Shim: CEF doesn't have window.chrome.webview. The runtime uses it
  // when present; if not, it falls back to window.wails.invoke.
  window.wails.invoke = function(msg) {
    if (typeof msg === "string") return wails_invoke(msg);
    return wails_invoke(JSON.stringify(msg));
  };

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