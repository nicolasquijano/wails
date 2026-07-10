//go:build linux && cgo && cef && !android && !server

package application

import _ "embed"

//go:embed cef_js_shim.js
var cefJSShimCode string

// cefJSShim returns the JS code that CEF::RegisterExtension installs as
// the "wails.cef" extension. The extension defines native functions on
// window.wails (and window._wails) that the runtime JS in
// internal/assetserver/bundledassets/runtime.js calls for IPC.
//
// See cef_v8_handler.go for the Go side of those functions.
func cefJSShim() string { return cefJSShimCode }